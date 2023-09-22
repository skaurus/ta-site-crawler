package queue

import (
	"errors"
	"fmt"

	"github.com/nutsdb/nutsdb"
)

type queue struct {
	nutsDB *nutsdb.DB
}

type Queue interface {
	Cleanup() error
	AddTask(string) error
	GetTask() (string, error)
}

var (
	ErrStringAlreadyInQueue = errors.New("string already in queue")
)

const (
	listBucket string = "crawlerLists"
	setBucket  string = "crawlerSets"
)

var (
	mainListKey = []byte("mainList")
	mainSetKey  = []byte("mainSet")
)

// Init opens existing queue or creates a new one and returns the queue instance
// Don't forget to call defer queue.Cleanup() in appropriate place!
func Init(workingDir string, startingURL string) (Queue, error) {
	// TODO: detect if queue did not exist previously and if so populate it with startingURL
	db, err := nutsdb.Open(
		nutsdb.DefaultOptions,
		nutsdb.WithDir(workingDir),
	)
	if err != nil {
		return nil, err
	}

	err = db.Update(
		func(tx *nutsdb.Tx) error {
			fmt.Printf("init\n")
			queueSize, err := tx.LSize(listBucket, mainListKey)
			if err != nil && !errors.Is(err, nutsdb.ErrListNotFound) {
				fmt.Printf("LSize failed\n")
				return err
			}
			if queueSize > 0 {
				return nil
			}

			val := []byte(startingURL)
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
	fmt.Printf("addTask\n")
	exists, err := tx.SIsMember(setBucket, mainSetKey, val)
	if err != nil && !errors.Is(err, nutsdb.ErrBucketNotFound) {
		fmt.Printf("SIsMember failed\n")
		return err
	}
	if exists {
		return ErrStringAlreadyInQueue
	}

	fmt.Printf("adding %s to queue\n", val)
	if err := tx.RPush(listBucket, mainListKey, val); err != nil {
		fmt.Printf("RPush failed\n")
		return err
	}
	if err := tx.SAdd(setBucket, mainSetKey, val); err != nil {
		fmt.Printf("SAdd failed\n")
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
	fmt.Printf("getTask\n")
	val, err = tx.LPop(listBucket, mainListKey)
	if err != nil {
		// nutsdb.ErrRecordIsNil should be exported error, but it is not...
		if err.Error() == "the record is nil" {
			return val, nil
		} else {
			fmt.Printf("LPop failed\n")
			return nil, err
		}
	}
	err = tx.SRem(setBucket, mainSetKey, val)
	if err != nil {
		fmt.Printf("SRem failed\n")
		return nil, err
	}

	fmt.Printf("got %s from queue\n", val)
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
