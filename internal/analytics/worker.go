package analytics

import (
	"fmt"
	"log"
	"sync"
)

type HubBroadcaster interface {
	BroadcastToRoom(docID string, message []byte)
}

// Job represents a background task for the worker pool.
type Job struct {
	DocID string
	Text  string
}

// WorkerPool manages a pool of goroutines for processing analytics.
type WorkerPool struct {
	jobs chan Job
	wg   sync.WaitGroup
	hub  HubBroadcaster
}

// NewWorkerPool initializes the channel.
func NewWorkerPool(bufferSize int, hub HubBroadcaster) *WorkerPool {
	return &WorkerPool{
		jobs: make(chan Job, bufferSize),
		hub:  hub,
	}
}

// Start spins up the requested number of worker goroutines.
func (p *WorkerPool) Start(numWorkers int) {
	for i := 1; i <= numWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	log.Printf("[WorkerPool] Started %d analytics workers", numWorkers)
}

// worker represents a single goroutine constantly pulling jobs.
func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		// Call our mathematical string analysis engine
		result := AnalyzeText(job.Text)

		log.Printf("[Analytics Worker #%d] Score: %.2f | Words: %d | Sentences: %d",
			id, result.ReadingScore, result.WordCount, result.SentenceCount)

		if p.hub != nil {
			msg := fmt.Sprintf(`{"type":"activity","payload":"[Analytics Engine] Document Scanned - Readability Score: %.2f (Words: %d)"}`, result.ReadingScore, result.WordCount)
			p.hub.BroadcastToRoom(job.DocID, []byte(msg))
		}
	}
}

// Submit enqueues a new job onto the unbuffered/buffered channel.
func (p *WorkerPool) Submit(docID, text string) {
	p.jobs <- Job{DocID: docID, Text: text}
}

// Stop closes the channel and safely waits for all workers to complete in-flight jobs.
func (p *WorkerPool) Stop() {
	close(p.jobs)
	log.Println("[WorkerPool] Waiting for workers to safely terminate...")
	p.wg.Wait()
	log.Println("[WorkerPool] Stopped successfully.")
}
