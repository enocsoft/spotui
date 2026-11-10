package app

// playerQueue serializes the player commands the UI issues. Update submits a
// task synchronously — so the order of the keypresses is exactly the order
// of execution — and returns a command that waits for the result in its own
// goroutine. The tasks themselves run one at a time, each in the tea.Cmd
// goroutine of the submit that started the in-flight job, so two rapid
// presses can never interleave inside the player.

import "sync"

type playerJob struct {
	task playerTask
	done chan playerMsg
}

type playerQueue struct {
	mu      sync.Mutex
	busy    bool // a task is running, or the queue is draining
	pending []playerJob
}

// submit queues the task and returns the channel its result lands on. It
// never runs the task and never blocks: empty queue or not, Update is back
// before the player is touched.
func (q *playerQueue) submit(task playerTask) <-chan playerMsg {
	q.mu.Lock()
	ch := make(chan playerMsg, 1)
	if q.busy {
		q.pending = append(q.pending, playerJob{task: task, done: ch})
		q.mu.Unlock()
		return ch
	}
	q.busy = true
	q.mu.Unlock()
	go q.drain(task, ch)
	return ch
}

// drain runs the task, delivers the result to whichever tea.Cmd goroutine is
// waiting, then takes whatever queued behind it. The drain role travels with
// the in-flight job — no permanent worker goroutine — and the pending slice
// keeps the submission order FIFO.
func (q *playerQueue) drain(task playerTask, ch chan<- playerMsg) {
	for {
		ch <- playerMsg{op: task.op, track: task.track, latchID: task.latchID, err: task.run()}
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.busy = false
			q.mu.Unlock()
			return
		}
		next := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		task, ch = next.task, next.done
	}
}
