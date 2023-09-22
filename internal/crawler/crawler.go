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
	"github.com/skaurus/ta-site-crawler/internal/settings"
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
	crawlingDir     = "crawled"
	rootFilename    = "index"
	filePermissions = 0644
	dirPermissions  = 0755
)

var (
	nextID          uint8 = 1
	tasksInProgress uint32

	cookieJar  *cookiejar.Jar
	httpClient *http.Client

	pauseBetweenJobs = 200 * time.Millisecond

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

func Init() (err error) {
	cookieJar, err = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		panic(fmt.Sprintf("can't create cookie jar: %v", err))
	}

	httpClient = &http.Client{
		Jar:     cookieJar,
		Timeout: time.Duration(settings.Get().HTTPTimeout()) * time.Second,
	}

	return nil
}

// SpawnWorkers spawns n workers and returns an error if any
// ctx is used to stop workers
// q is a queue to get urls from
// outputDir is a directory to save results
// n is a number of workers to spawn
func SpawnWorkers(ctx context.Context, wg *sync.WaitGroup, q queue.Queue, runtimeSettings settings.Settings) error {
	for i := uint8(0); i < runtimeSettings.WorkersCnt(); i++ {
		w := newWorker(q, runtimeSettings.OutputDir()+"/"+crawlingDir)
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
	logger := settings.Get().Logger()

	logger.Info().Uint8("workerID", w.id).Msg("worker is started")

	for {
		select {
		case <-ctx.Done():
			logger.Info().Uint8("workerID", w.id).Msg("worker is done")
			wg.Done()
			return
		default:
			// let's not hammer our queue with requests
			time.Sleep(pauseBetweenJobs)

			err := w.work()
			if err != nil {
				if errors.Is(err, ErrNoWorkToDo) {
					logger.Info().Uint8("workerID", w.id).Msg("worker has no work to do")
					// let's check if anything at all is at work right now
					// if there are no jobs and no one is doing anything — we can stop
					// (I'm not completely happy with this solution — bc now we have ctx,
					// wg and atomic uint at the same time. I was thinking about storing
					// "working on" set in db, but that would pose its own problems)
					if tasksInProgress == 0 {
						logger.Info().Uint8("workerID", w.id).Msg("worker is done")
						wg.Done()
						return
					}
				}
				logger.Error().Uint8("workerID", w.id).Err(err).Msg("worker got an error")
			}
		}
	}
}

func (w *worker) work() (err error) {
	logger := settings.Get().Logger()

	defer func() {
		if err := recover(); err != nil {
			logger.Error().Uint8("workerID", w.id).Any("recover", err).Msg("worker recovered from panic")
		}
	}()

	urlString, err := w.q.GetTask()
	if err != nil {
		return err
	}
	if len(urlString) == 0 {
		return ErrNoWorkToDo
	}
	logger.Info().Uint8("workerID", w.id).Str("task", urlString).Msg("worker got a task")

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
		logger.Error().Uint8("workerID", w.id).Err(err).Str("task", urlString).Msg("worker can't parse an url")
		return err
	}
	if !urlObject.IsAbs() {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("task", urlString).Msg("worker got not an absolute url")
		return nil
	}

	// convert URL path to a file path and name
	// path could be empty, file name could be empty as well (e.g., https://example.com/
	// or https://example.com/path/), but we will handle it
	logger.Debug().Uint8("workerID", w.id).Str("urlPath", urlObject.Path).Msg("worker got url path")
	urlPath := strings.TrimPrefix(strings.TrimSuffix(urlObject.Path, "/"), "/")
	urlPathElements := strings.Split(urlPath, "/")
	filename := urlPathElements[len(urlPathElements)-1]
	filenameWasEmpty := false
	if len(filename) == 0 {
		filenameWasEmpty = true
		filename = rootFilename // we will try to add relevant extension later
	}
	subfolder := strings.Join(urlPathElements[:len(urlPathElements)-1], "/")
	logger.Debug().Uint8("workerID", w.id).Str("urlPath", urlObject.Path).Str("filename", filename).Str("subfolder", subfolder).Msg("worker got filename and subfolder from url")
	fullFilename := w.outputDir + "/" + subfolder + "/" + filename

	// check if the file is already downloaded; if it is, there is nothing to do
	err = os.MkdirAll(w.outputDir+"/"+subfolder, dirPermissions)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("folder", w.outputDir+"/"+subfolder).Msg("worker can't create path folder")
		return err
	}
	if _, err := os.Stat(fullFilename); err == nil {
		logger.Error().Uint8("workerID", w.id).Str("fullFilename", fullFilename).Msg("worker found existing file, skipping")
		return err
	}

	resp, err := httpClient.Get(urlString)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Msg("worker got an http error")
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300; !statusOK {
		logger.Warn().Uint8("workerID", w.id).Int("statusCode", resp.StatusCode).Msg("worker got bad http status code")
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
		logger.Warn().Uint8("workerID", w.id).Str("contentType", contentType).Str("urlString", urlString).Msg("worker got a non-text content-type")
		return nil
	}
	fullFilenameWithoutExt := ""
	// besides filenameWasEmpty case, we can have non-empty filenames without
	// the extension. let's make them prettier too
	if !strings.Contains(filename, ".") {
		filename = filename + "." + fileExt
		fullFilenameWithoutExt = fullFilename
		fullFilename = fullFilename + "." + fileExt
	}

	// os.O_CREATE|os.O_EXCL requires file to not exist
	tempFile, err := os.OpenFile(fullFilename+".temp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePermissions)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("fullFilename_temp", fullFilename+".temp").Msg("worker can't create a temp file")
		return err
	}
	// io.Copy directly to tempFile would be nice, but we will need the body later
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Msg("worker can't read response body")
		return err
	}
	_, err = tempFile.Write(body)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("fullFilename_temp", fullFilename+".temp").Msg("worker can't write response body to a temp file")
		return err
	}

	// now we can atomically rename the file
	err = os.Rename(fullFilename+".temp", fullFilename)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("fullFilename_temp", fullFilename+".temp").Str("fullFilename", fullFilename).Msg("worker can't rename temp file")
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
	err = w.q.MarkAsProcessed(urlString)
	if err != nil {
		logger.Error().Uint8("workerID", w.id).Err(err).Str("urlString", urlString).Msg("worker can't mark url as processed")
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
		logger.Error().Uint8("workerID", w.id).Err(err).Str("urlString", urlString).Msg("worker can't parse html")
		return err
	}

	foundURLs := make([]string, 0)
	// https://pkg.go.dev/golang.org/x/net/html#example-Parse
	var parseNode func(*html.Node)
	parseNode = func(n *html.Node) {
		if n.Type == html.ElementNode {
			lookingForAttr, ok := tags2LinkAttribute[n.Data]
			if ok {
				for _, a := range n.Attr {
					if a.Key == lookingForAttr {
						foundURLs = append(foundURLs, a.Val)
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

	workingHost := utils.UrlToHost(urlObject)
	for _, foundURL := range foundURLs {
		newUrlObject, err := url.Parse(foundURL)
		if err != nil {
			logger.Error().Uint8("workerID", w.id).Err(err).Str("foundURL", foundURL).Msg("worker can't parse found url")
			continue
		}

		newUrlObject = urlObject.ResolveReference(newUrlObject)
		newUrlObject, err = utils.NormalizeUrlObject(newUrlObject)
		if err != nil {
			logger.Error().Uint8("workerID", w.id).Err(err).Str("foundURL", foundURL).Msg("worker can't parse normalized version of found url")
		}

		if utils.UrlToHost(newUrlObject) != workingHost {
			continue
		}

		isProcessed, err := w.q.IsProcessed(foundURL)
		if err != nil {
			logger.Error().Uint8("workerID", w.id).Err(err).Str("foundURL", foundURL).Msg("worker can't check if found url is processed")
		}
		if isProcessed {
			continue
		}

		err = w.q.AddTask(foundURL)
		if err != nil {
			logger.Error().Uint8("workerID", w.id).Err(err).Str("foundURL", foundURL).Msg("worker can't add found url to queue")
		}
	}

	return nil
}
