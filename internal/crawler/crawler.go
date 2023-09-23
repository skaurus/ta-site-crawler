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

	"github.com/rs/zerolog"
	"golang.org/x/net/context"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"

	"github.com/skaurus/ta-site-crawler/internal/queue"
	"github.com/skaurus/ta-site-crawler/internal/settings"
	"github.com/skaurus/ta-site-crawler/internal/utils"
)

type worker struct {
	id     uint8
	q      queue.Queue
	logger *zerolog.Logger
}

type Worker interface {
	Run(ctx context.Context, wg *sync.WaitGroup)
}

var (
	ErrNoWorkToDo = errors.New("no work to do")
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
		w := newWorker(q)
		go w.Run(ctx, wg)
	}

	return nil
}

func newWorker(q queue.Queue) (w Worker) {
	// I do this instead of using directly nextID to lessen the risks of someone
	// in the future incidentally using nextID _after it was incremented_.
	// `id` will always be safe to use.
	id := nextID
	logger := settings.Get().Logger().With().Uint8("workerID", id).Logger()
	nextID++ // use `id` var instead of me, please! 🥹

	return &worker{
		id:     id,
		q:      q,
		logger: &logger,
	}
}

func (w *worker) Run(ctx context.Context, wg *sync.WaitGroup) {
	w.logger.Info().Uint8("workerID", w.id).Msg("worker is started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Info().Msg("worker is done")
			wg.Done()
			return
		default:
			// let's not hammer our queue with requests
			time.Sleep(pauseBetweenJobs)

			err := w.work()
			if err != nil {
				if errors.Is(err, ErrNoWorkToDo) {
					w.logger.Info().Msg("worker has no work to do")
					// let's check if anything at all is at work right now
					// if there are no jobs and no one is doing anything — we can stop
					// (I'm not completely happy with this solution — bc now we have ctx,
					// wg and atomic uint at the same time. I was thinking about storing
					// "working on" set in db, but that would pose its own problems)
					if tasksInProgress == 0 {
						w.logger.Info().Msg("worker is done")
						wg.Done()
						return
					}
				}
				w.logger.Error().Err(err).Msg("worker got an error")
			}
		}
	}
}

func (w *worker) work() (err error) {
	defer func() {
		if err := recover(); err != nil {
			w.logger.Error().Any("recover", err).Msg("worker recovered from panic")
		}
	}()

	urlString, err := w.q.GetTask()
	if err != nil {
		return err
	}
	if len(urlString) == 0 {
		return ErrNoWorkToDo
	}
	w.logger.Info().Str("task", urlString).Msg("worker got a task")

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
		w.logger.Error().Err(err).Str("task", urlString).Msg("worker can't parse an url")
		return err
	}
	if !urlObject.IsAbs() {
		w.logger.Error().Err(err).Str("task", urlString).Msg("worker got not an absolute url")
		return nil
	}

	// let's convert URL path to a file path and name, where we will store
	// the crawled document
	w.logger.Debug().Str("urlPath", urlObject.Path).Msg("converting this path to file structure")
	path, filename, err := utils.UrlToFileStructure(urlObject)
	w.logger.Debug().Str("urlPath", urlObject.Path).Str("path", path).Str("filename", filename).Msg("given path amounted to this file structure")
	if err != nil {
		w.logger.Error().Err(err).Str("path", path).Msg("worker can't create path folder")
		return err
	}
	// if this is the case, we will later try to append a proper file extension to it
	filenameWasEmpty := filename == settings.RootFilename
	fullPath := settings.Get().OutputDir() + "/" + settings.CrawlingDir + "/" + path
	fullFilename := fullPath + "/" + filename

	err = os.MkdirAll(fullPath, settings.DirPermissions)
	if err != nil {
		w.logger.Error().Err(err).Str("folder", fullPath).Msg("can't create folder")
		return
	}

	// check if the file is already downloaded; if it is, there is nothing to do
	if _, err := os.Stat(fullFilename); err == nil {
		w.logger.Error().Str("fullFilename", fullFilename).Msg("worker found existing file, skipping")
		return nil
	}

	resp, err := httpClient.Get(urlString)
	if err != nil {
		w.logger.Error().Err(err).Msg("worker got an http error")
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300; !statusOK {
		w.logger.Warn().Int("statusCode", resp.StatusCode).Msg("worker got bad http status code")
		return nil
	}

	contentType := resp.Header.Get("Content-Type")
	// now, content-type will likely be something like "text/html; charset=utf-8",
	// and we are interested in just "text/html". also, I'm pretty sure in the wild
	// big Internet it can be something like "text/html ;charset = utf-8" and browsers
	// will still work. maybe it could be even worse
	contentTypeParts := strings.Split(contentType, ";")
	contentType = strings.ToLower(strings.TrimSpace(contentTypeParts[0]))
	fileExt, ok := allowedContentTypes2Ext[contentType]
	if !ok {
		w.logger.Warn().Str("contentType", contentType).Str("urlString", urlString).Msg("worker got a non-text content-type")
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
	tempFile, err := os.OpenFile(fullFilename+".temp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, settings.FilePermissions)
	if err != nil {
		w.logger.Error().Err(err).Str("fullFilename_temp", fullFilename+".temp").Msg("worker can't create a temp file")
		return err
	}
	// io.Copy directly to tempFile would be nice, but we will need the body later
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		w.logger.Error().Err(err).Msg("worker can't read response body")
		return err
	}
	_, err = tempFile.Write(body)
	if err != nil {
		w.logger.Error().Err(err).Str("fullFilename_temp", fullFilename+".temp").Msg("worker can't write response body to a temp file")
		return err
	}

	// now we can atomically rename the file
	err = os.Rename(fullFilename+".temp", fullFilename)
	if err != nil {
		w.logger.Error().Err(err).Str("fullFilename_temp", fullFilename+".temp").Str("fullFilename", fullFilename).Msg("worker can't rename temp file")
		return err
	}
	// to make that early exit above ("found existing file, skipping") work, we
	// will write a marker file
	if filenameWasEmpty {
		// I feel that this edge case is not such a big deal to stop working on the task
		// that's why I ignore the error
		_ = os.WriteFile(
			fullFilenameWithoutExt,
			[]byte(fmt.Sprintf("princess is in another castle: %s.%s\n(this is a marker file, please do not delete it)", settings.RootFilename, fileExt)),
			settings.FilePermissions,
		)
	}
	err = w.q.MarkAsProcessed(urlString)
	if err != nil {
		w.logger.Error().Err(err).Str("urlString", urlString).Msg("worker can't mark url as processed")
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
		w.logger.Error().Err(err).Str("urlString", urlString).Msg("worker can't parse html")
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
			w.logger.Error().Err(err).Str("foundURL", foundURL).Msg("worker can't parse found url")
			continue
		}

		newUrlObject = urlObject.ResolveReference(newUrlObject)
		newUrlObject, err = utils.NormalizeUrlObject(newUrlObject)
		if err != nil {
			w.logger.Error().Err(err).Str("foundURL", foundURL).Msg("worker can't parse normalized version of found url")
		}

		if utils.UrlToHost(newUrlObject) != workingHost {
			continue
		}

		isProcessed, err := w.q.IsProcessed(foundURL)
		if err != nil {
			w.logger.Error().Err(err).Str("foundURL", foundURL).Msg("worker can't check if found url is processed")
		}
		if isProcessed {
			continue
		}

		err = w.q.AddTask(foundURL)
		if err != nil {
			w.logger.Error().Err(err).Str("foundURL", foundURL).Msg("worker can't add found url to queue")
		}
	}

	return nil
}
