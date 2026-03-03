package runnergroup

import (
	"encoding/json"
	"sync"
	"time"
)

// RunnerGroup is like sync.WaitGroup,
// the diffrence is if one task stops, all will be stopped.
type RunnerGroup struct {
	Runners       []*Runner
	once_stop     func()
	start_results map[int]error
	stop_results  map[int]error
	mutex         *sync.Mutex
}

// Each Start and Stop in Runner will be called once
type Runner struct {
	// Start is a blocking function.
	Start func() error
	// Stop is not a blocking function, if Stop called, must let Start return.
	Stop func() error
}

func New() *RunnerGroup {
	g := &RunnerGroup{}
	g.Runners = make([]*Runner, 0)
	g.start_results = map[int]error{}
	g.stop_results = map[int]error{}
	g.mutex = &sync.Mutex{}
	return g
}

func (g *RunnerGroup) Add(r *Runner) {
	g.Runners = append(g.Runners, r)
}

// Call Wait after all tasks have been added,
func (g *RunnerGroup) Wait() error {
	var wg1 sync.WaitGroup
	wg1.Add(len(g.Runners))
	g.once_stop = sync.OnceFunc(func() {
		time.Sleep(3 * time.Second)
		for i, v := range g.Runners {
			go func(i int, v *Runner) {
				defer wg1.Done()
				err := v.Stop()
				g.mutex.Lock()
				g.stop_results[i] = err
				g.mutex.Unlock()
			}(i, v)
		}
	})

	var wg sync.WaitGroup
	wg.Add(len(g.Runners))
	for i, v := range g.Runners {
		go func(i int, v *Runner) {
			defer wg.Done()
			err := v.Start()
			g.mutex.Lock()
			g.start_results[i] = err
			g.mutex.Unlock()
			g.once_stop()
		}(i, v)
	}
	wg.Wait()
	wg1.Wait()

	g.mutex.Lock()
	defer g.mutex.Unlock()
	e := &Error{
		Start: make([]string, len(g.Runners)),
		Stop:  make([]string, len(g.Runners)),
	}
	ok := true
	for i, v := range g.start_results {
		if v == nil {
			e.Start[i] = ""
		}
		if v != nil {
			e.Start[i] = v.Error()
		}
		if ok && v != nil {
			ok = false
		}
	}
	for i, v := range g.stop_results {
		if v == nil {
			e.Stop[i] = ""
		}
		if v != nil {
			e.Stop[i] = v.Error()
		}
		if ok && v != nil {
			ok = false
		}
	}
	if ok {
		return nil
	}
	return e
}

// Call Done to stop all tasks.
func (g *RunnerGroup) Done() error {
	if g.once_stop != nil {
		g.once_stop()
	}
	// compatible
	return nil
}

type Error struct {
	Start []string
	Stop  []string
}

func (e *Error) Error() string {
	b, err := json.Marshal(e)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
