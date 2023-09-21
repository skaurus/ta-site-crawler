## Context and Problem Statement

How to store the list of URLs to be crawled?
* It should be reliable and persistent
* It should be embeddable / self-contained, so pure Go solution is preferred
* It should support pop and push operations in a convenient way
* But it should not be "overweight" like a RDBMS
* And it should be as simple as possible — doing just one thing right — and do not have a lot of dependencies

## Considered Options

* text file with manual locking, URL per line
* text file with gob-encoded slice, written on exit, read on startup
* SQLite
* Redis
* [NutsDB](https://github.com/nutsdb/nutsdb)
* [Go port of LevelDB](https://github.com/syndtr/goleveldb)
* [Bolt](https://github.com/boltdb/bolt)
* [goconcurrentqueue](https://github.com/enriquebris/goconcurrentqueue)
* [queue](https://github.com/adrianbrad/queue)
* [deque #1](https://github.com/adrianbrad/queue)
* [deque #2](https://github.com/gammazero/deque)
* [bigqueue](https://github.com/jhunters/bigqueue)
* [dque](https://github.com/joncrlsn/dque)

Remember, KV storage is not enough for us, that filters out a lot of Golang databases. (Redis is not purely KV storage, by the way. Redis is a lot of things.)

## Decision Outcome

And the winner is... NutsDB. And we will store the DB in the domain subfolder.

Text files with URL per line are too fragile, persistence is under question, and they are not web-scale.
Text files with gob-encoded slices are too fragile, persistence is under question, and slices are not thread-safe.
SQLite is too cumbersome to build.
Redis is an external dependency, overkill for our use case.
NutsDB — not sure about the project maturity.
Go port of LevelDB does not seem to be in a good technical shape.
Bolt does not support lists.
goconcurrentqueue is not persistent and can become slow.
queue is not persistent.
deque #1 is not persistent.
deque #2 is not persistent.
bigqueue has anemic tests.
dque does not seem to be reliable.

### Consequences

Well, for some reason, I can't trust it completely, but we will see how it goes. If it works for us — fine, if not — we will return to the drawing board...

## Pros and Cons of the Options

### text file, URL per line

* Good
    * because it is simple
    * because it is very human-readable, and even editable if it is necessary!
* Bad
    * because it will probably involve a lot of locking and hidden bugs/complexity
        * remember — we will have multiple writes in our multithreaded case
    * [not web-scale](https://www.youtube.com/watch?v=b2F-DItXtZs)

### text file, gob-encoded slice

* Good
    * because it is still quite simple
* Bad
    * slices are not thread-safe
    * persists to disk only on exit (in a simpler implementation)

### SQLite

* Good
    * perfect reputation
    * ACID
    * all the SQL you want
    * thread-safe
    * persistent
    * we probably already have SQL database in our stack, and this approach will be easier to port to it
* Bad
    * requires cgo and can compile for a LONG time

### Redis

* Good
    * great reputation
    * data structures and operations over them for every taste
    * thread-safe
    * persistent
    * we probably already have it in our stack
* Bad
    * requires Redis server to be installed and configured

I love Redis, but this is definitely overkill. I would strongly prefer a self-contained solution, and Redis must be installed, configured and maintained separately. This is actually easy in case of Redis, but it is still an additional moving part.

### NutsDB

* Good
    * pure Go
    * actively maintained
    * 3k+ GitHub stars, 300+ forks
    * a lot of nice badges (joking not joking)
    * moderate number of dependencies
    * has list datatype
    * it seems implied that it is thread-safe
    * it doesn't seem to have a lot of reliability-related open issues (I even checked chinese ones)
    * seems to have nice [benchmark numbers](https://github.com/nutsdb/nutsdb/blob/master/docs/user_guides/benchmarks.md)... (for kv operations though)
    * persistent
* Bad
    * just haven't heard about it / it being used in production
    * it doesn't seem mature enough in general

### Go port of LevelDB

* Good
    * pure Go
    * 5.8k GitHub stars, 900+ forks
    * used by a LOT of other projects
    * thread-safe
    * persistent
    * uses golangci
* Bad
    * "build is failing" flare
    * the last (and only) release was 4+ years ago
    * has a lot of open issues, some of them seem quite serious
    * it doesn't seem to be maintained

### Bolt

* Good
    * pure Go
    * seems to be moderately actively maintained
    * 13.7k+ GitHub stars, 1.5+ forks
    * some nice badges (joking not joking)
    * used by a LOT of other projects
    * not trying to do everything, actually it is declared complete
    * 14k GitHub stars
    * thread-safe
    * only 3 kloc
    * database file can be used only by a single process == lock file
    * persistent
* Bad
    * only KV :(
    * not maintained anymore though
    * does not use go modules

"only KV" immediately disqualifies it for us, although otherwise it ticks all my boxes.

### goconcurrentqueue

* Good
    * pure Go
    * seems to be moderately actively maintained
    * 300+ GitHub stars, 30 forks
    * a lot of nice badges (joking not joking)
    * modest dependencies
    * thread-safe
* Bad
    * persistence is not included
    * performance degrades seriously when the queue is big (even 100 items, but 1000 is even worse, and it seems exponential)

### queue

* Good
    * pure Go
    * seems to be actively maintained
    * 200 GitHub stars, 10+ forks
    * a lot of nice badges (joking not joking)
    * no dependencies at all
    * thread-safe
    * fast
    * uses generics
    * uses golangci
    * tests look fairly extensive
* Bad
    * persistence is not included

### deque #1

* Good
    * pure Go
    * seems to be moderately actively maintained
    * 140 GitHub stars, 7 forks
    * no dependencies at all
    * thread-safe
    * fast
    * v2 uses generics
    * uses golangci
    * tests look fairly extensive
* Bad
    * persistence is not included

### deque #2

* Good
    * pure Go
    * seems to be moderately actively maintained
    * 500 GitHub stars, 50+ forks
    * no dependencies at all
    * uses generics
    * tests look fairly extensive
* Bad
    * persistence is not included
    * not thread-safe — "up to the application, if it needs it at all"

### bigqueue

* Good
    * pure Go
    * seems to be moderately actively maintained
    * 40+ GitHub stars, 20 forks
    * a lot of nice badges (joking not joking)
    * almost no dependencies
    * thread-safe
    * persistent
* Bad
    * tests are anemic
    * rather slow, though enough for our use case

### dque

* Good
    * pure Go
    * seems to be not so actively maintained
    * 700+ GitHub stars, 40 forks
    * a lot of nice badges (joking not joking)
    * not a lot of dependencies
    * thread-safe
    * persistent
    * tests look fairly extensive
* Bad
    * quite a lot of open issues and PRs, issues mention data corruption