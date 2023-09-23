package settings

import (
	"net/url"

	"github.com/rs/zerolog"
)

type settings struct {
	urlObject   *url.URL
	outputDir   string
	workersCnt  uint8
	logger      *zerolog.Logger
	httpTimeout uint16
}

type Settings interface {
	URL() *url.URL
	OutputDir() string
	WorkersCnt() uint8
	Logger() *zerolog.Logger
	HTTPTimeout() uint16
}

var settingsInstance Settings

const (
	DirPermissions  = 0755
	FilePermissions = 0644
	CrawlingDir     = "crawled"
	RootFilename    = "_index"
)

// Save saves settings to singleton instance; also it kinda works as a getter,
// if someone tries to call it again.
func Save(urlObject *url.URL, outputDir string, workersCnt uint8, logger *zerolog.Logger, httpTimeout uint16) Settings {
	if settingsInstance == nil {
		settingsInstance = &settings{
			urlObject:   urlObject,
			outputDir:   outputDir,
			workersCnt:  workersCnt,
			logger:      logger,
			httpTimeout: httpTimeout,
		}
	} else {
		settingsInstance.Logger().Error().Msg("settings were already saved, returning existing instance")
	}

	return settingsInstance
}

func Get() Settings {
	if settingsInstance == nil {
		panic("settings were not saved yet")
	}
	return settingsInstance
}

func (s *settings) URL() *url.URL {
	return s.urlObject
}

func (s *settings) OutputDir() string {
	return s.outputDir
}

func (s *settings) WorkersCnt() uint8 {
	return s.workersCnt
}

func (s *settings) Logger() *zerolog.Logger {
	return s.logger
}

func (s *settings) HTTPTimeout() uint16 {
	return s.httpTimeout
}
