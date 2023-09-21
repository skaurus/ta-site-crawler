## Context and Problem Statement

From the requirements, we must support resuming the crawling of the site. Also, my sense of beauty tells me that:
* we should support storing multiple sites in the same output directory
* we should be able to crawl them in parallel (by running multiple instances of a crawler)
* already downloaded documents == state using which we can resume the crawling
* we should actively disallow crawling the same site from multiple instances of a crawler

## Decision Outcome

* We will use subfolders for each site
* Subfolder name will be derived from the site domain
* Site domain should be normalized (lowercased, without www. part, and don't forget about international domains)
* Downloaded documents should be stored keeping the path structure (each path component is a subfolder)
* We will have our own ("system") files in there, for example - a lock file, log file, pid file, list of URLs to be crawled, etc
  * It is important to avoid even the possibility of the file name collision between downloaded documents and our system files