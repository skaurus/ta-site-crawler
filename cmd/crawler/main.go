package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/purell"
	"github.com/spf13/pflag"
	"golang.org/x/net/idna"
)

var (
	urlFlagValue string
	parsedURL    *url.URL
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
	parsedURL, err = url.Parse(urlFlagValue)
	if err != nil {
		reportFlagsError("--url/-u flag value must be a valid URL")
	}
	if !parsedURL.IsAbs() {
		reportFlagsError("--url/-u flag value must be an absolute URL")
	}
	// unfortunately, purell lib returns only strings, not an *url.URL
	normalizedURL := purell.NormalizeURL(parsedURL, purell.FlagsSafe)
	parsedURL, err = url.Parse(normalizedURL)
	if err != nil {
		panic(fmt.Sprintf("can't parse normalized url %s: %s", normalizedURL, err.Error()))
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

	// TODO this piece of code is a good candidate to be moved to a separate function
	host, port := parsedURL.Hostname(), parsedURL.Port()
	// totally not necessary, but I think that makes a user life easier when
	// he looks for his crawling results
	punycode := idna.New(idna.StrictDomainName(true))
	host, err = punycode.ToUnicode(host)
	if err != nil {
		panic(fmt.Sprintf("can't convert host %s to unicode: %s", host, err.Error()))
	}
	subfolder := strings.Join(strings.Split(host, "."), "_")
	// theoretically, some crazy person can use http scheme on port 443 AND serve
	// different content than on port 80. in this case, we will make a mistake of
	// choosing the same subfolder. but I don't want to be too nitpicky in TA
	if len(port) > 0 && port != "80" && port != "443" {
		subfolder = fmt.Sprintf("%s_%s", subfolder, port)
	}
	outputDir = outputDir + "/" + subfolder

	err = os.Mkdir(outputDir, 0755)
	if err != nil && !os.IsExist(err) {
		panic(fmt.Sprintf("can't create subfolder %s: %s", outputDir, err.Error()))
	}
	fmt.Printf("using %s as a crawler output dir\n", outputDir)
}

func main() {
	// the logic of a crawler will look like this:
	// 1. try to open a file with persisted crawling queue
	// 1.1 if it doesn't exist, create one with one element — the url from flags
	// 2. start requested number of goroutines
	// 3. each goroutine will:
	// 3.1 get an url from the queue
	// 3.2 try to determine the content-type of the url (let's believe that HTTP headers do not lie)
	// 3.3 if it is a text format
	// 3.3.1 create subfolders structure mimicking the url path
	// 3.3.2 check if we already have the file with the same name in the subfolder; if yes - goto 3.1
	// 3.3.2 save the content of the url to the file in the subfolder with the .temp suffix
	// 3.3.2.1 if the file with the .temp suffix already exists - overwrite it
	// 3.3.3 if fetching is successful - rename the file to remove .temp suffix
	// 3.3.4 if this file content type was html, try to parse it and find all links from the same domain
	// 3.3.5 push all found links to the queue, but only if they are not already there
	// 3.4 do some bookkeeping to track interesting stat

	// TODO
	// react to 3* and 4* HTTP status codes
	// figure out what to do if some goroutine gets the url; starts writing .temp file; other subroutine adds the same url to the queue (it is not in a queue right now! it seems to be unique!); some goroutine starts working on that url as well, overwriting .temp file; BOOM
}

func reportFlagsError(errText string) {
	fmt.Println(errText)
	pflag.Usage()
	os.Exit(1)
}
