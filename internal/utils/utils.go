package utils

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/purell"
	"github.com/rs/zerolog"
	"golang.org/x/net/idna"

	"github.com/skaurus/ta-site-crawler/internal/settings"
)

func UrlToHost(urlObject *url.URL) string {
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
		// we should either log error here, or return it to the caller
		// first option creates circular dependency;
		// second one makes calling code more cumbersome.
		// at this point I'm in a hurry and going to skip it 😅
		// TODO: return error to the caller
	}

	if len(port) > 0 {
		host = host + ":" + port
	}

	return host
}

// DomainToOutputFolder returns the name of the folder for a given domain; this
// folder will hold all the files crawled from this domain, and our system files.
// That allows to have multiple crawlers working in parallel, given they crawl
// different sites.
func DomainToOutputFolder(urlObject *url.URL) string {
	host := UrlToHost(urlObject)
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
func UrlToFileStructure(logger *zerolog.Logger, urlObject *url.URL) (path, filename string, err error) {
	logger.Debug().Str("urlPath", urlObject.Path).Msg("converting this path to file structure")

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
	logger.Debug().Str("urlPath", urlObject.Path).Str("path", path).Str("filename", filename).Msg("given path amounted to this file structure")

	return
}
