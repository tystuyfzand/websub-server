package websub

import "meow.tf/websub/model"

// PublishJob represents a job to publish data to a subscription.
type PublishJob struct {
	Subscription model.Subscription `json:"subscription"`
	ContentType  string             `json:"contentType"`
	Data         []byte             `json:"data"`
}

// Worker is an interface to allow other types of workers to be created.
type Worker interface {
	Add(f PublishJob)
	Start()
	Stop()
}

// NewGoWorker creates a new worker from the specified hub and worker count.
func NewGoWorker(h *Hub, workerCount int) *GoWorker {
	return &GoWorker{
		hub:         h,
		workerCount: workerCount,
		jobCh:       make(chan PublishJob),
	}
}

// GoWorker is a basic Goroutine-based worker.
// It will start workerCount workers and process jobs from a channel.
type GoWorker struct {
	hub         *Hub
	workerCount int
	jobCh       chan PublishJob
}

// Add will add a job to the queue.
func (w *GoWorker) Add(job PublishJob) {
	w.jobCh <- job
}

// Start will start the worker routines.
func (w *GoWorker) Start() {
	for i := 0; i < w.workerCount; i++ {
		go w.run()
	}
}

// Stop will close the job channel, causing each worker routine to exit.
func (w *GoWorker) Stop() {
	close(w.jobCh)
}

// run pulls jobs off the job channel and processes them.
func (w *GoWorker) run() {
	for {
		job, ok := <-w.jobCh

		if !ok {
			return
		}

		w.hub.callCallback(job)
	}
}
