package queue

import (
	"errors"
	"fmt"

	"github.com/nutsdb/nutsdb"

	"github.com/skaurus/ta-site-crawler/internal/settings"
)

type queue struct {
	nutsDB *nutsdb.DB
}

type Queue interface {
	Cleanup() error
	AddTask(string) error
	GetTask() (string, error)
	MarkAsProcessed(string) error
	IsProcessed(string) (bool, error)
}

var (
	ErrStringAlreadyInQueue = errors.New("string already in queue")
)

const (
	listBucket string = "crawlerLists"
	setBucket  string = "crawlerSets"
)

var (
	mainListKey     = []byte("mainList")
	mainSetKey      = []byte("mainSet")
	processedSetKey = []byte("processedSet")
)

// Init opens existing queue or creates a new one and returns the queue instance
// Don't forget to call defer queue.Cleanup() in appropriate place!
func Init(runtimeSettings settings.Settings) (Queue, error) {
	logger := runtimeSettings.Logger()

	db, err := nutsdb.Open(
		nutsdb.DefaultOptions,
		nutsdb.WithDir(runtimeSettings.OutputDir()),
	)
	if err != nil {
		return nil, err
	}

	err = db.Update(
		func(tx *nutsdb.Tx) error {
			fmt.Printf("init\n")
			queueSize, err := tx.LSize(listBucket, mainListKey)
			if err != nil && !errors.Is(err, nutsdb.ErrListNotFound) {
				logger.Debug().Err(err).Msg("LSize failed")
				return err
			}
			if queueSize > 0 {
				return nil
			}

			val := []byte(runtimeSettings.URL().String())
			return addTask(tx, val)
		},
	)

	return &queue{
		nutsDB: db,
	}, err
}

func (q *queue) Cleanup() error {
	return q.nutsDB.Close()
}

func addTask(tx *nutsdb.Tx, val []byte) error {
	logger := settings.Get().Logger()

	logger.Debug().Msg("addTask")
	exists, err := tx.SIsMember(setBucket, mainSetKey, val)
	if err != nil && !errors.Is(err, nutsdb.ErrBucketNotFound) {
		logger.Debug().Err(err).Msg("SIsMember failed")
		return err
	}
	if exists {
		return ErrStringAlreadyInQueue
	}

	logger.Debug().Str("val", string(val)).Msg("adding to queue")
	if err := tx.RPush(listBucket, mainListKey, val); err != nil {
		logger.Debug().Err(err).Msg("RPush failed")
		return err
	}
	if err := tx.SAdd(setBucket, mainSetKey, val); err != nil {
		logger.Debug().Err(err).Msg("SAdd failed")
		return err
	}

	return nil
}

func (q *queue) AddTask(value string) (err error) {
	err = q.nutsDB.Update(
		func(tx *nutsdb.Tx) error {
			val := []byte(value)

			return addTask(tx, val)
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func getTask(tx *nutsdb.Tx) (val []byte, err error) {
	logger := settings.Get().Logger()

	logger.Debug().Msg("getTask")
	val, err = tx.LPop(listBucket, mainListKey)
	if err != nil {
		// nutsdb.ErrRecordIsNil should be exported error, but it is not...
		if err.Error() == "the record is nil" {
			return val, nil
		} else {
			logger.Debug().Err(err).Msg("LPop failed")
			return nil, err
		}
	}
	err = tx.SRem(setBucket, mainSetKey, val)
	if err != nil {
		logger.Debug().Err(err).Msg("SRem failed")
		return nil, err
	}

	logger.Debug().Str("val", string(val)).Msg("got from queue")
	return val, nil
}

func (q *queue) GetTask() (value string, err error) {
	var val []byte
	err = q.nutsDB.Update(
		func(tx *nutsdb.Tx) error {
			val, err = getTask(tx)
			return err
		},
	)
	if err != nil {
		return "", err
	}

	return string(val), nil
}

func (q *queue) MarkAsProcessed(value string) (err error) {
	err = q.nutsDB.Update(
		func(tx *nutsdb.Tx) error {
			val := []byte(value)
			return tx.SAdd(setBucket, processedSetKey, val)
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (q *queue) IsProcessed(value string) (isProcessed bool, err error) {
	err = q.nutsDB.View(
		func(tx *nutsdb.Tx) error {
			val := []byte(value)
			isProcessed, err = tx.SIsMember(setBucket, processedSetKey, val)
			return err
		},
	)
	if err != nil {
		if errors.Is(err, nutsdb.ErrBucketNotFound) {
			return false, nil
		}
		return false, err
	}

	return isProcessed, nil
}
