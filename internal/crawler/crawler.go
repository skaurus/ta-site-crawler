package crawler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"

	"github.com/skaurus/ta-site-crawler/internal/queue"
	"github.com/skaurus/ta-site-crawler/internal/utils"
)

type worker struct {
	id        uint8
	q         queue.Queue
	outputDir string
}

type Worker interface {
	Run(ctx context.Context, wg *sync.WaitGroup)
}

var (
	ErrNoWorkToDo = errors.New("no work to do")
)

const (
	rootFilename    = "index"
	filePermissions = 0644
	dirPermissions  = 0755
)

var (
	nextID          uint8 = 1
	tasksInProgress uint32

	cookieJar  *cookiejar.Jar
	httpClient *http.Client

	// https://stackoverflow.com/a/48704300/320345
	allowedContentTypes2Ext = map[string]string{
		"text/html":              "html",
		"text/css":               "css",
		"application/javascript": "js",
		"text/javascript":        "js",
		"text/plain":             "txt",
		"application/json":       "json",
		"application/xml":        "xml",
		"text/xml":               "xml",
	}

	// the list is not complete, see https://stackoverflow.com/a/2725168/320345
	// but let's try to find a compromise between completeness and number of lines
	// also, of course we are interested in attributes that we can reasonably expect
	// to contain a link to a text document
	tags2LinkAttribute = map[string]string{
		"a":          "href",
		"blockquote": "cite",
		"iframe":     "src",
		"link":       "href",
		"script":     "src",
	}
)

func init() {
	var err error
	cookieJar, err = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		panic(fmt.Sprintf("can't create cookie jar: %v", err))
	}

	httpClient = &http.Client{
		Jar:     cookieJar,
		Timeout: 5 * time.Second, // TODO make it configurable with flag
	}
}

// SpawnWorkers spawns n workers and returns an error if any
// ctx is used to stop workers
// q is a queue to get urls from
// outputDir is a directory to save results
// n is a number of workers to spawn
func SpawnWorkers(ctx context.Context, wg *sync.WaitGroup, q queue.Queue, outputDir string, n uint8) error {
	outputDir = outputDir + "/crawled"
	for i := uint8(0); i < n; i++ {
		w := newWorker(q, outputDir)
		go w.Run(ctx, wg)
	}

	return nil
}

func newWorker(q queue.Queue, outputDir string) (w Worker) {
	w = &worker{
		id:        nextID,
		q:         q,
		outputDir: outputDir,
	}
	nextID++

	return w
}

func (w *worker) Run(ctx context.Context, wg *sync.WaitGroup) {
	fmt.Printf("worker %d is started\n", w.id)

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("worker %d is done\n", w.id)
			wg.Done()
			return
		default:
			// let's not hammer our queue with requests
			time.Sleep(100 * time.Millisecond)

			err := w.work()
			if err != nil {
				if errors.Is(err, ErrNoWorkToDo) {
					fmt.Printf("worker %d has no work to do\n", w.id)
					// let's check if anything at all is at work right now
					// if there are no jobs and no one is doing anything — we can stop
					// (I'm not completely happy with this solution — bc now we have ctx,
					// wg and atomic uint. I was thinking about storing "working on" set
					// in NutsDB, but that would pose its own reliability problems)
					if tasksInProgress == 0 {
						fmt.Printf("worker %d is done\n", w.id)
						wg.Done()
						return
					}
				}
				fmt.Printf("worker %d got an error: %v\n", w.id, err)
			}
		}
	}
}

func (w *worker) work() (err error) {
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("worker %d recovered from: %v\n", w.id, err)
		}
	}()

	urlString, err := w.q.GetTask()
	if err != nil {
		return err
	}
	if len(urlString) == 0 {
		return ErrNoWorkToDo
	}
	fmt.Printf("worker %d got a task: %s\n", w.id, urlString)

	atomic.AddUint32(&tasksInProgress, 1)
	// 🤯, but the "smart guys" say that "every" programmer should know what
	// a two's complement is and this is "basic" knowledge. anyway:
	// https://pkg.go.dev/sync/atomic#AddUint32
	// https://groups.google.com/g/golang-nuts/c/5GtQGOZgjHM?pli=1
	// https://en.wikipedia.org/wiki/Two's_complement
	defer func() { atomic.AddUint32(&tasksInProgress, ^uint32(0)) }()

	// TODO do some bookkeeping to track interesting stat

	urlObject, err := url.Parse(urlString)
	if err != nil {
		fmt.Printf("worker %d can't parse an url [%s]: %v\n", w.id, urlString, err)
		return err
	}
	if !urlObject.IsAbs() {
		fmt.Printf("worker %d got not an absolute url: %s\n", w.id, urlString)
		return err
	}

	// convert URL path to a file path and name
	// path could be empty, file name could be empty as well (e.g., https://example.com/
	// or https://example.com/path/), but we will handle it
	fmt.Printf("worker %d got an url with path %s\n", w.id, urlObject.Path)
	urlPath := strings.TrimPrefix(strings.TrimSuffix(urlObject.Path, "/"), "/")
	urlPathElements := strings.Split(urlPath, "/")
	filename := urlPathElements[len(urlPathElements)-1]
	filenameWasEmpty := false
	if len(filename) == 0 {
		filenameWasEmpty = true
		filename = rootFilename // we will try to add relevant extension later
	}
	subfolder := strings.Join(urlPathElements[:len(urlPathElements)-1], "/")
	fmt.Printf("worker %d got an url with filename %s and subfolder %s\n", w.id, filename, subfolder)
	fullFilename := w.outputDir + "/" + subfolder + "/" + filename

	// check if the file is already downloaded; if it is, there is nothing to do
	err = os.MkdirAll(w.outputDir+"/"+subfolder, dirPermissions)
	if err != nil {
		fmt.Printf("worker %d can't create folder %s/%s: %v\n", w.id, w.outputDir, subfolder, err)
		return err
	}
	if _, err := os.Stat(fullFilename); err == nil {
		fmt.Printf("worker %d found existing file %s, skipping\n", w.id, fullFilename)
		return err
	}

	resp, err := httpClient.Get(urlString)
	if err != nil {
		fmt.Printf("worker %d got an http error: %v\n", w.id, err)
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300; !statusOK {
		fmt.Printf("worker %d got a bad http status code: %d\n", w.id, resp.StatusCode)
		return nil
	}

	contentType := resp.Header.Get("Content-Type")
	// now, content-type could be something like "text/html; charset=utf-8"
	// and I'm pretty sure it can be "text/html ;charset = utf-8" and browsers
	// will still work. maybe it could be even worse
	if len(contentType) > 0 {
		contentTypeParts := strings.Split(contentType, ";")
		contentType = strings.TrimSpace(contentTypeParts[0])
	}
	fileExt, ok := allowedContentTypes2Ext[contentType]
	if !ok {
		fmt.Printf("worker %d got a non-text content-type [%s] for [%s]\n", w.id, contentType, urlString)
		return nil
	}
	fullFilenameWithoutExt := ""
	if filenameWasEmpty {
		filename = filename + "." + fileExt
		fullFilenameWithoutExt = fullFilename
		fullFilename = fullFilename + "." + fileExt
	}

	// os.O_CREATE|os.O_EXCL requires file to not exist
	tempFile, err := os.OpenFile(fullFilename+".temp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions)
	if err != nil {
		fmt.Printf("worker %d can't create a temp file %s.temp: %v\n", w.id, fullFilename, err)
		return err
	}
	// io.Copy directly to tempFile would be nice, but we will need the body later
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("worker %d can't read response body: %v\n", w.id, err)
		return err
	}
	_, err = tempFile.Write(body)
	if err != nil {
		fmt.Printf("worker %d can't write response body to a temp file %s.temp: %v\n", w.id, fullFilename, err)
		return err
	}

	// now we can atomically rename the file
	err = os.Rename(fullFilename+".temp", fullFilename)
	if err != nil {
		fmt.Printf("worker %d can't rename temp file %s.temp to %s: %v\n", w.id, fullFilename, fullFilename, err)
		return err
	}
	// to make that early exit above ("found existing file, skipping") work, we
	// will write a marker file
	if filenameWasEmpty {
		// I feel that this edge case is not such a big deal to stop working on the task
		// that's why I ignore the error
		_ = os.WriteFile(
			fullFilenameWithoutExt,
			[]byte(fmt.Sprintf("princess is in another castle: %s.%s\n(this is a marker file, please do not delete it)", rootFilename, fileExt)),
			filePermissions,
		)
	}

	// now we need to parse the body and find all links from the same domain.
	// of course, in production I would write a simple regexp to do this... /sarcasm
	// https://stackoverflow.com/a/1732454/320345 never gets old
	// on a serious note, we will try to parse only the text/html documents
	if contentType != "text/html" {
		return nil
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		fmt.Printf("worker %d can't parse html from %s: %v\n", w.id, urlString, err)
		return err
	}
	// https://pkg.go.dev/golang.org/x/net/html#example-Parse
	var parseNode func(*html.Node)
	parseNode = func(n *html.Node) {
		if n.Type == html.ElementNode {
			lookingForAttr, ok := tags2LinkAttribute[n.Data]
			if ok {
				for _, a := range n.Attr {
					if a.Key == lookingForAttr {
						newUrlObject, err := url.Parse(a.Val)
						// I don't like nested conditions like that, usually I prefer
						// an early exit style. But here we have break in the end,
						// and I don't want to copy it to each early exit point even more
						if err != nil {
							fmt.Printf("worker %d can't parse an url [%s]: %v\n", w.id, a.Val, err)
						} else {
							newUrlObject = urlObject.ResolveReference(newUrlObject)
							newUrlObject, err = utils.NormalizeUrlObject(newUrlObject)
							if err != nil {
								fmt.Printf("worker %d can't parse normalized version of url [%s]: %v\n", w.id, a.Val, err)
							} else {
								err = w.q.AddTask(newUrlObject.String())
								if err != nil {
									fmt.Printf("worker %d can't add a task [%s]: %v\n", w.id, newUrlObject.String(), err)
								}
							}
						}
						break
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			parseNode(child)
		}
	}
	parseNode(doc)

	return nil
}
