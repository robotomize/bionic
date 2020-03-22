package main

import (
	"encoding/json"
	"fmt"
	"github.com/robotomize/bionic"
	"github.com/valyala/fastrand"
	"math"
	"net/http"
	"os"
	"time"
)

// pi launches n goroutines to compute an

// approximation of pi.
func pi(n int) float64 {
	ch := make(chan float64)
	for k := 0; k <= n; k++ {
		go term(ch, float64(k))
	}
	f := 0.0
	for k := 0; k <= n; k++ {
		f += <-ch
	}
	return f
}

func term(ch chan float64, k float64) {
	ch <- 4 * math.Pow(-1, k) / (2*k + 1)
}

type PiJobReq struct {
	N int `json:"n"`
}

type PiJobResp struct {
	Pi float64 `json:"pi"`
}

func NewServer() *bionic.Manager {
	b := bionic.New()
	b.AddHook("pi", func(bytes []byte) error {
		file, openErr := os.OpenFile("./examples/response.json", os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0755))
		if openErr != nil {
			fmt.Printf(openErr.Error())
		}
		defer func() {
			if err := file.Close(); err != nil {
				fmt.Printf(err.Error())
			}
		}()
		if _, err := fmt.Fprint(file, string(bytes)+"\n"); err != nil {
			fmt.Printf(err.Error())
		}
		// send to file
		return nil
	}, func(bytes []byte) error {
		if _, err := fmt.Fprint(os.Stdout, string(bytes)+"\n"); err != nil {
			fmt.Printf(err.Error())
		}
		// send to stdout
		return nil
	})
	b.Serve()
	return b
}

func NewClient() {
	c, err := bionic.NewClient("ws://localhost:9090/ws", http.Header{"Cookie": []string{}})
	if err != nil {
		fmt.Printf(err.Error())
		os.Exit(2)
	}
	c.RegisterHandlers("pi", func(j *bionic.JobMessage) error {
		var req *PiJobReq
		if err := json.Unmarshal(j.Job.Payload, &req); err != nil {
			return err
		}
		n := req.N
		res := &PiJobResp{Pi: pi(n)}

		bytes, err := json.Marshal(&res)
		if err != nil {
			return err
		}
		j.Proto.Kind = bionic.JobCompletedMessageKind
		j.Job.Payload = bytes
		return nil
	})
	go c.Listen()
}

func main() {
	b := NewServer()
	NewClient()
	for {
		j := &PiJobReq{
			N: int(fastrand.Uint32n(5000)),
		}

		payload, jobReqErr := json.Marshal(j)
		if jobReqErr != nil {
			fmt.Printf(jobReqErr.Error())
		}

		b.AddJob(bionic.NewJob("pi", payload, 30))
		time.Sleep(10 * time.Millisecond)
	}
}
