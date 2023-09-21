## Context and Problem Statement

We want to be sure that we do not crawl the same site from multiple instances.

## Considered Options

* lock file
* pid file

## Decision Outcome

The winner is... pid file, because it is easier to check if the process behind is still running. For lock files we would need some strategy of dealing with stale lock files.