package analytics

import (
	"log"
	"sync"
)

// Job represents a background task for the worker pool.
type Job struct {
	Text string
}

// WorkerPool manages a pool of goroutines for processing analytics.
type WorkerPool struct {
	jobs chan Job
	wg   sync.WaitGroup
}

// NewWorkerPool initializes the channel.
func NewWorkerPool(bufferSize int) *WorkerPool {
	return &WorkerPool{
		jobs: make(chan Job, bufferSize),
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
	}
}

// Submit enqueues a new job onto the unbuffered/buffered channel.
func (p *WorkerPool) Submit(text string) {
	p.jobs <- Job{Text: text}
}

// Stop closes the channel and safely waits for all workers to complete in-flight jobs.
func (p *WorkerPool) Stop() {
	close(p.jobs)
	log.Println("[WorkerPool] Waiting for workers to safely terminate...")
	p.wg.Wait()
	log.Println("[WorkerPool] Stopped successfully.")
}
