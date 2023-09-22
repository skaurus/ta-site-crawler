package utils

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/purell"
	"golang.org/x/net/idna"
)

// UrlToOutputFolder returns the name of the folder for a given domain
func UrlToOutputFolder(urlObject *url.URL) string {
	host, port := urlObject.Hostname(), urlObject.Port()

	// totally not necessary, but I think that makes a user life easier when
	// he looks for his crawling results (but FS must support unicode)
	punycode := idna.New(idna.StrictDomainName(true))
	host, err := punycode.ToUnicode(host)
	if err != nil {
		panic(fmt.Sprintf("can't convert host %s to unicode: %v", host, err))
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
