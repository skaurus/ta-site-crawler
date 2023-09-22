This repository is a solution to a test assigment that goes like this:

> Implement a web crawler that would be recursively downloading the given site (following the links).
> Crawler should download the document by the given URL and continue downloading by the links found in the document.
>
> Crawler should support resuming the download.
> Crawler should download only text documents - html, css, js (ignore images, videos, etc.).
> Crawler should download documents only from the same domain (ignore external links).
> Crawler should be multithreaded (which parts to parallelize - is up to you).
>
> Requirements are given informally on purpose.
> We want to see how you will make decisions on your own, what is more important and what is less.
>
> We expect a working application that we can build and run.
> We do not expect correct handling of all error types and boundary cases, you should set the "good enough" bar yourself.
>
> There are no restrictions on 3rd-party libraries.

## Solution

[![Go Report Card](https://goreportcard.com/badge/github.com/skaurus/ta-site-crawler)](https://goreportcard.com/report/github.com/skaurus/ta-site-crawler)

Please see my reasoning in the [docs/madr](docs/madr) folder.
[MADR](https://adr.github.io/madr/) is `a lean template to capture any decisions in a structured way`.

I find that that kind of stuff - reasoning at some point of time - is usually slips through the cracks, and few years later, in a different context, looking at a different surrounding code, it is difficult to reason why certain things was done in a certain way back then.

So, recently I decided to look for solutions that would augment the code to solve it, and stumbled upon the MADR.

But the most important bits — which are, IMHO, goals and non-goals — I will list right here:
- Goals:
  - [x] Recursively follow the links to the same domain (protocol and www subdomain are ignored)
  - [x] Do not download the same document twice
  - [x] Crawler work can be interrupted by Ctrl-C at any time (or it can crash...)
  - [x] Crawler must be able to resume the crawling after such an event
  - [x] Download only text documents - html, css, js
  - [x] Download only from the same domain
  - [x] Parse only the statically existing links (that were present in the HTML given to us by the server)
  - [x] Have reasonable timeouts on all requests
  - [x] Have a log file
  - [x] Support running on *nix systems
  - [x] Be multithreaded
  - [x] Be able to react adequately to the following errors:
    - [x] URL is unreachable (initial one or any of the found ones)
    - [x] output directory is... not a directory, not writable, etc
    - [x] crawling subfolder in output directory is not a directory, not writable, or has strange content not from our crawler, or that content seems to be broken
- Possible goals:
  - [ ] Display what our threads are doing nicely in a console
  - [ ] Also, maybe show some runtime stats, like number of documents downloaded, number of links to be processed, average (median?) server response time and download speed etc
  - [ ] Have a nice CLI interface
  - [ ] Support running on Windows (it would mostly involve path handling, I think)
- Non goals:
  - [ ] Resuming the download of the same document (that would be important to support if we were to support media formats)
  - [ ] Support crawling SPA/Ajax sites (that would require a headless browser and a lot of headaches)

## How to build and run

Building is easy:
`go build -o crawler ./cmd/crawler`

Running is not so hard either:
`./crawler --url https://bbcgoodfood.com --workers 10 --output-dir ~/crawled-sites -с`

Crawler will create a subfolder inside a given output directory, and will download all the documents there. Also, it will be able to resume work if such subfolder already exists.

`--log-to-stdout/-c` (`c` for console) flag will make it log to STDOUT for better visibility. Also you can set the HTTP requests timeout with `--http-timeout/-t` flag (default is 5 seconds).