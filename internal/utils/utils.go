package utils

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/purell"
	"golang.org/x/net/idna"

	"github.com/skaurus/ta-site-crawler/internal/settings"
)

func UrlToHost(urlObject *url.URL) string {
	logger := settings.Get().Logger()

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
		logger.Error().Err(err).Str("host", host).Msg("can't punycode host")
	}

	if len(port) > 0 {
		host = host + ":" + port
	}

	return host
}

// UrlToOutputFolder returns the name of the folder for a given domain
func UrlToOutputFolder(urlObject *url.URL) string {
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
