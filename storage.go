package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/trustnetworks/analytics-common/cloudstorage"
	dt "github.com/trustnetworks/analytics-common/datatypes"
	"github.com/trustnetworks/analytics-common/utils"
	"github.com/trustnetworks/analytics-common/worker"

	"github.com/google/uuid"
)

const pgm = "storage"

var maxBatch int64
var maxTime float64

type work struct {
	storage      cloudstorage.CloudStorage // Platform-specific storage
	project      string                     // Currently unused
	basedir      string
	data         []byte
	count        int64
	last         time.Time
	stripPayload bool
}

func setMaxBatchSize() {
	var err error

	// Default file size, if no batch size env value set
	// Default of 64M is optimal value for our data
	var defaultMaxBatch int64 = 67108864 // 64 * 1024 * 1024
	var mBytes = false
	var kBytes = false

	// Get batch size env value and trim, remove spaces and M or K
	mBatchFromEnv := utils.Getenv("MAX_BATCH", "67108864")
	mBatch := strings.Replace(mBatchFromEnv, "\"", "", -1)
	if strings.Contains(strings.ToUpper(mBatch), "M") {
		mBatch = strings.Replace(strings.ToUpper(mBatch), "M", "", -1)
		mBytes = true
	} else if strings.Contains(strings.ToUpper(mBatch), "K") {
		mBatch = strings.Replace(strings.ToUpper(mBatch), "K", "", -1)
		kBytes = true
	}
	mBatch = strings.Replace(mBatch, " ", "", -1)
	mBatch = strings.TrimSpace(mBatch)

	// Check max batch size value set in env is parsable to int, if not use default value
	maxBatch, err = strconv.ParseInt(mBatch, 10, 64)
	if err != nil {
		maxBatch = defaultMaxBatch
		utils.Log("Couldn't parse MAX_BATCH: %v :using default %v", mBatchFromEnv, defaultMaxBatch)

	} else {
		if mBytes == true {
			if maxBatch < ((math.MaxInt64 / 1024) / 1024) {
				maxBatch = maxBatch * 1024 * 1024
			} else {
				utils.Log("Couldn't convert MAX_BATCH to Megabytes: %v :using default %v", mBatchFromEnv, defaultMaxBatch)
			}
		} else if kBytes == true {
			if maxBatch < (math.MaxInt64 / 1024) {
				maxBatch = maxBatch * 1024
			} else {
				utils.Log("Couldn't convert MAX_BATCH to Kilobytes: %v :using default %v", mBatchFromEnv, defaultMaxBatch)
			}
		}

	}

	utils.Log("maxBatch set to: %v", maxBatch)
}

func setMaxTime() {
	var err error

	// Default max time if no max time env values set
	// Default of 30 mins is optimal for our data
	var defaultMaxTime float64 = 1800 // 30 mins

	// Get max time env value and trim, remove spaces
	mTimeFromEnv := utils.Getenv("MAX_TIME", "1800")
	mTime := strings.Replace(mTimeFromEnv, "\"", "", -1)
	mTime = strings.Replace(mTime, " ", "", -1)
	mTime = strings.TrimSpace(mTime)

	// Check max time value set in env is parsable to int, if not use default value
	maxTime, err = strconv.ParseFloat(mTime, 64)
	if err != nil {
		utils.Log("Couldn't parse MAX_TIME: %v :using default %v", mTimeFromEnv, defaultMaxTime)
		maxTime = defaultMaxTime
	}

	utils.Log("maxTime set to: %v", maxTime)
}

func (s *work) init() error {

	setMaxBatchSize()
	setMaxTime()

	s.project = utils.Getenv("STORAGE_PROJECT", "")
	s.basedir = utils.Getenv("STORAGE_BASEDIR", "cyberprobe")

	s.count = 0
	s.last = time.Now()
	s.stripPayload = utils.Getenv("STRIP_PAYLOAD", "false") == "true"

	s.storage = cloudstorage.New(utils.Getenv("PLATFORM", ""))
	s.storage.Init("STORAGE_BUCKET", "")

	return nil
}

func (s *work) Handle(msg []uint8, w *worker.Worker) error {

	var e dt.Event

	// Convert JSON object to internal object.
	err := json.Unmarshal(msg, &e)
	if err != nil {
		utils.Log("Couldn't unmarshal json: %s", err.Error())
		return nil
	}
	changed := false
	if e.Action == "unrecognised_stream" {
		e.UnrecognisedStream.PayloadB64Length = len(e.UnrecognisedStream.Payload)
		if s.stripPayload {
			e.UnrecognisedStream.Payload = ""
		}
		changed = true
	} else if e.Action == "unrecognised_datagram" {
		e.UnrecognisedDatagram.PayloadB64Length = len(e.UnrecognisedDatagram.Payload)
		if s.stripPayload {
			e.UnrecognisedDatagram.Payload = ""
		}
		changed = true
	} else if s.stripPayload {
		switch e.Action {
		case "icmp":
			e.Icmp.Payload = ""
			changed = true
			break
		case "http_request":
			e.HttpRequest.Body = ""
			changed = true
			break
		case "http_response":
			e.HttpResponse.Body = ""
			changed = true
			break
		case "sip_request":
			e.SipRequest.Payload = ""
			changed = true
			break
		case "sip_response":
			e.SipResponse.Payload = ""
			changed = true
			break
		case "smtp_data":
			e.SmtpData.Data = ""
			changed = true
			break
		}
	}

	if changed {
		msg, err = json.Marshal(&e)
		if err != nil {
			utils.Log("JSON marshal failed: %s", err.Error())
			return nil
		}
	}

	s.data = append(s.data[:], []byte(msg)[:]...)
	s.data = append(s.data[:], []byte{'\n'}[:]...)

	s.count += int64(len(msg))

	// Whichever occurs first: either we've exceeded our batch size or the
	// timeout between batches. It is the platform-specific upload function's
	// responsibility to upload the data safely within platform-specific limits
	if (s.count > maxBatch) || (time.Since(s.last).Seconds() > maxTime) {

		uuid := uuid.New().String()

		// FIXME: Think I want to do GMT here.
		tm := time.Now().Format("2006-01-02/15-04")

		path := s.basedir + "/" + tm + "/" + uuid
		s.storage.Upload(path, s.data)

		s.data = []byte{}
		s.count = 0
		s.last = time.Now()

	}

	return nil

}

func main() {

	var w worker.QueueWorker
	var s work
	utils.LogPgm = pgm

	utils.Log("Initialising...")

	err := s.init()
	if err != nil {
		utils.Log("init: %s", err.Error())
		return
	}

	var input string
	var output []string

	if len(os.Args) > 0 {
		input = os.Args[1]
	}
	if len(os.Args) > 2 {
		output = os.Args[2:]
	}

	// context to handle control of subroutines
	ctx := context.Background()
	ctx, cancel := utils.ContextWithSigterm(ctx)
	defer cancel()

	err = w.Initialise(ctx, input, output, pgm)
	if err != nil {
		utils.Log("init: %s", err.Error())
		return
	}

	utils.Log("Initialisation complete.")

	// Invoke Wye event handling.
	err = w.Run(ctx, &s)
	if err != nil {
		utils.Log("error: Event handling failed with err: %s", err.Error())
	}

}
