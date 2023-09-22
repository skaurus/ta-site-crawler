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

	"github.com/spf13/pflag"

	"github.com/skaurus/ta-site-crawler/internal/crawler"
	"github.com/skaurus/ta-site-crawler/internal/queue"
	"github.com/skaurus/ta-site-crawler/internal/utils"
)

var (
	urlFlagValue string
	urlObject    *url.URL
	outputDir    string
	workersCnt   uint8
)

func init() {
	pflag.StringVarP(&urlFlagValue, "url", "u", "", "valid url where to start crawling")
	pflag.StringVarP(&outputDir, "output-dir", "d", "", "output directory to save results")
	pflag.Uint8VarP(&workersCnt, "workers", "w", 1, "number of workers to work in parallel")

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
}

func main() {
	// this method tries to open already existing queue, or if it does not exist —
	// creates a new one and populates it with provided starting URL
	q, err := queue.Init(outputDir, urlObject.String())
	if err != nil {
		panic(fmt.Sprintf("can't initialize queue: %v", err))
	}
	defer func() {
		err := q.Cleanup()
		if err != nil {
			fmt.Printf("can't cleanup queue: %v", err)
		}
	}()

	// facility to gracefully interrupt the program execution
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	// production system would also catch SIGHUP to reopen the logfile to allow for logrotate

	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(int(workersCnt))
	// this method starts requested number of goroutines
	// ctx is used to stop them
	// wg is used to wait for them to finish
	err = crawler.SpawnWorkers(ctx, &wg, q, outputDir, workersCnt)
	if err != nil {
		cancel() // just in case
		panic(fmt.Sprintf("can't spawn workers: %v", err))
	}

forLoop:
	for {
		select {
		case sig := <-sigCh:
			fmt.Printf("got signal %s, exiting...\n", sig)
			cancel()
			break forLoop
		}
	}
	close(sigCh)

	wg.Wait()
	fmt.Printf("exited\n")

	// TODO
	// figure out what to do if some goroutine gets the url; starts writing .temp file; other subroutine adds the same url to the queue (it is not in a queue right now! it seems to be unique!); some goroutine starts working on that url as well, overwriting .temp file; BOOM (or not BOOM? what's so bad in occasional overwriting?)
	// log to file
	// currently we can ask goroutines to stop from the main. we need to be able to ask main to exit from goroutines
	// filter out links to other sites
	// we need to have a set of already successfully processed urls, otherwise we will be adding them over and over while parsing heavy sites
}

func reportFlagsError(errText string) {
	fmt.Println(errText)
	pflag.Usage()
	os.Exit(1)
}
