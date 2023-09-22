package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"

	"github.com/skaurus/ta-site-crawler/internal/crawler"
	"github.com/skaurus/ta-site-crawler/internal/queue"
	"github.com/skaurus/ta-site-crawler/internal/settings"
	"github.com/skaurus/ta-site-crawler/internal/utils"
)

var (
	runtimeSettings settings.Settings
)

const (
	logFilename = "crawler.log"
)

func init() {
	var (
		urlFlagValue string
		urlObject    *url.URL
		outputDir    string
		workersCnt   uint8
		logToStdout  bool
		httpTimeout  uint16
	)

	pflag.StringVarP(&urlFlagValue, "url", "u", "", "valid url where to start crawling")
	pflag.StringVarP(&outputDir, "output-dir", "d", "", "output directory to save results")
	pflag.Uint8VarP(&workersCnt, "workers", "w", 1, "number of workers to work in parallel")
	pflag.BoolVarP(&logToStdout, "log-to-stdout", "c", false, "log to stdout instead of file")
	pflag.Uint16VarP(&httpTimeout, "http-timeout", "t", 5, "HTTP timeout in seconds")

	pflag.Parse()

	if len(urlFlagValue) == 0 {
		reportFlagsError("--url/-u flag is required")
	}
	var err error
	urlObject, err = url.Parse(urlFlagValue)
	if err != nil {
		reportFlagsError("--url/-u flag value must be a valid URL")
	}
	if !urlObject.IsAbs() {
		reportFlagsError("--url/-u flag value must be an absolute URL")
	}
	urlObject, err = utils.NormalizeUrlObject(urlObject)
	if err != nil {
		panic(fmt.Sprintf("can't parse normalized version of url %s: %v", urlFlagValue, err))
	}

	if len(outputDir) == 0 {
		reportFlagsError("--output-dir/-d flag is required")
	}
	fileInfo, err := os.Stat(outputDir)
	if err != nil || !fileInfo.IsDir() {
		reportFlagsError("--output-dir/-d flag value must be a valid directory")
	}
	outputDir, err = filepath.Abs(outputDir)
	if err != nil {
		panic(fmt.Sprintf("can't get absolute path for %s", outputDir))
	}

	subfolder := utils.UrlToOutputFolder(urlObject)
	outputDir = outputDir + "/" + subfolder

	err = os.Mkdir(outputDir, 0755)
	if err != nil && !os.IsExist(err) {
		panic(fmt.Sprintf("can't create subfolder %s: %v", outputDir, err))
	}
	fmt.Printf("using %s as a crawler output dir\n", outputDir)

	// we should have a setting for dev/prod environment, and on prod we should
	// log from level Error or something like that
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	if logToStdout {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	} else {
		logFullPath := outputDir + "/" + logFilename
		logFile, err := os.OpenFile(logFullPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(fmt.Sprintf("can't create logfile %s: %v", logFullPath, err))
		}
		log.Logger = zerolog.New(logFile).With().Timestamp().Logger()
	}
	fmt.Printf("logfile is %s inside output dir\n", logFilename)

	runtimeSettings = settings.Save(urlObject, outputDir, workersCnt, &log.Logger, httpTimeout)
}

func main() {
	logger := runtimeSettings.Logger()

	// this method tries to open already existing queue, or if it does not exist —
	// creates a new one and populates it with provided starting URL
	q, err := queue.Init(runtimeSettings)
	if err != nil {
		panic(fmt.Sprintf("can't initialize queue: %v", err))
	}
	defer func() {
		err := q.Cleanup()
		if err != nil {
			logger.Error().Err(err).Msg("can't cleanup queue")
		}
	}()

	// facility to gracefully interrupt the program execution
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// production system would also catch SIGHUP to reopen the logfile to allow for logrotate

	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(int(runtimeSettings.WorkersCnt()))
	// this method starts requested number of goroutines
	// ctx is used to stop them
	// wg is used to wait for them to finish
	err = crawler.SpawnWorkers(ctx, &wg, q, runtimeSettings)
	if err != nil {
		cancel() // just in case
		panic(fmt.Sprintf("can't spawn workers: %v", err))
	}

forLoop:
	for {
		select {
		case sig := <-sigCh:
			logger.Warn().Any("sig", sig).Msg("got signal, exiting...")
			cancel()
			break forLoop
		}
	}
	close(sigCh)

	wg.Wait()
	logger.Warn().Msg("exited")

	// TODO
	// currently we can ask goroutines to stop from the main. we need to be able to ask main to exit from goroutines
}

func reportFlagsError(errText string) {
	fmt.Println(errText)
	pflag.Usage()
	os.Exit(1)
}
