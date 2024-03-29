package utils

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/PuerkitoBio/purell"
	"golang.org/x/exp/maps"
	"golang.org/x/net/idna"

	"github.com/skaurus/ta-site-crawler/internal/settings"
)

func UrlToHost(urlObject *url.URL) (string, error) {
	host, port := urlObject.Hostname(), urlObject.Port()
	if urlObject.Scheme == "http" && port == "80" {
		port = ""
	} else if urlObject.Scheme == "https" && port == "443" {
		port = ""
	}
	host, _ = strings.CutPrefix(host, "www.")

	// totally not necessary, but I think that makes a user life easier when
	// he looks for his crawling results (but FS must support unicode)
	punycode := idna.New(idna.StrictDomainName(true))
	punycodeHost, err := punycode.ToUnicode(host)
	if err == nil {
		host = punycodeHost
	} else {
		return "", fmt.Errorf("punycode.ToUnicode failed: %w", err)
	}

	if len(port) > 0 {
		host = host + ":" + port
	}

	return host, nil
}

// DomainToOutputFolder returns the name of the folder for a given domain; this
// folder will hold all the files crawled from this domain, and our system files.
// That allows to have multiple crawlers working in parallel, given they crawl
// different sites.
func DomainToOutputFolder(urlObject *url.URL) string {
	host, err := UrlToHost(urlObject)
	if err != nil {
		// QoL feature I introduced in UrlToHost — to have unicode folders for
		// sites instead of xn--... — makes our code fallible to additional errors
		// in cases I've tested it works. in practice that could be a mistake 🤔
		panic(fmt.Sprintf("can't work with this domain: %v", err))
	}

	var port string
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		host = parts[0]
		port = parts[1]
	}

	subfolder := strings.Join(strings.Split(host, "."), "_")
	// theoretically, some crazy person can use http scheme on port 443 AND serve
	// different content than on port 80. in this case, we will make a mistake of
	// choosing the same subfolder. but I don't want to be too nitpicky in TA
	if len(port) > 0 && port != "80" && port != "443" {
		subfolder = fmt.Sprintf("%s_%s", subfolder, port)
	}

	return subfolder
}

func NormalizeUrlObject(urlObject *url.URL) (*url.URL, error) {
	// unfortunately, purell lib returns only strings, not an *url.URL
	normalizedURL := purell.NormalizeURL(urlObject, purell.FlagsSafe)
	return url.Parse(normalizedURL)
}

// UrlToFileStructure converts URL path to a file path and name
func UrlToFileStructure(urlObject *url.URL) (path, filename string) {
	// path could be empty, filename could be empty as well (e.g., https://example.com/
	// or https://example.com/path/), but we will handle it
	urlPath := strings.TrimPrefix(strings.TrimSuffix(urlObject.Path, "/"), "/")
	urlPathElements := strings.Split(urlPath, "/")
	filename = urlPathElements[len(urlPathElements)-1]
	if len(filename) == 0 {
		// we have possible filename collisions here (different documents served
		// from /, /_index (with, say, content-type text/html), /_index.html).
		// with the `_index` instead of just `index` it is not so likely though.
		// also, it is not so obvious how to fix that when we do not yet know
		// what filenames we actually will receive from the server.
		// so let's just hope it will be alright and fix it when we actually hit
		// the problem, instead of creating complexity from the get go.
		filename = settings.RootFilename
	}
	path = strings.Join(urlPathElements[:len(urlPathElements)-1], "/")

	// some symbols are allowed in URL paths, but not necessarily in filesystem names
	// so far I know about one such symbol, asterisk (*), which is not allowed on Windows
	path = strings.ReplaceAll(path, "*", "_")
	filename = strings.ReplaceAll(filename, "*", "_")

	// also, we need to make unique filenames for different sets of GET parameters
	var params = urlObject.Query()
	sortedParamNames := maps.Keys(params)
	slices.Sort(sortedParamNames)
	paramStrings := make([]string, 0, len(sortedParamNames))
	for _, paramName := range sortedParamNames {
		paramValues := params[paramName]
		paramStrings = append(paramStrings, fmt.Sprintf("%s-%s", paramName, strings.Join(paramValues, "-")))
	}
	if len(paramStrings) > 0 {
		filename = fmt.Sprintf("%s__%s", filename, strings.Join(paramStrings, "_"))
	}

	return
}
