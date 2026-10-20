package worker

import "time"

// SetQueueOpTimeout overrides the queue operation timeout, returning a function to restore it
func SetQueueOpTimeout(d time.Duration) func() {
	orig := queueOpTimeout
	queueOpTimeout = d
	return func() { queueOpTimeout = orig }
}
