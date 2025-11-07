package chiplet

// Scheduler captures host-side orchestration logic for the chiplet platform.
// Concrete implementations will manage task graphs, resource allocation, and
// cross-chiplet synchronization.
type TaskExecutor interface {
	ExecuteTask(task *Task)
}

type Scheduler interface {
	Init(config *Config, topology *Topology, executor TaskExecutor)
	Fini()
	EnqueueTask(task *Task)
	Tick()
	IsIdle() bool
}

// taskQueue is a simple FIFO used by the basic scheduler implementation.
type taskQueue struct {
	items []*Task
}

func (this *taskQueue) enqueue(task *Task) {
	this.items = append(this.items, task)
}

func (this *taskQueue) dequeue() (*Task, bool) {
	if len(this.items) == 0 {
		return nil, false
	}

	task := this.items[0]
	this.items[0] = nil
	this.items = this.items[1:]

	return task, true
}

func (this *taskQueue) isEmpty() bool {
	return len(this.items) == 0
}

// BasicScheduler provides a minimal runnable implementation that drains a FIFO
// queue. It is intended to be extended with dependency management and resource
// modelling in later phases.
type BasicScheduler struct {
	config   *Config
	topology *Topology
	executor TaskExecutor

	queue      taskQueue
	nextTaskID int
}

func (this *BasicScheduler) Init(config *Config, topology *Topology, executor TaskExecutor) {
	this.config = config
	this.topology = topology
	this.executor = executor
	this.queue = taskQueue{items: make([]*Task, 0)}
	this.nextTaskID = 0
}

func (this *BasicScheduler) Fini() {
	this.queue.items = nil
	this.executor = nil
}

func (this *BasicScheduler) EnqueueTask(task *Task) {
	if task == nil {
		return
	}

	if task.ID == 0 {
		this.nextTaskID++
		task.ID = this.nextTaskID
	}

	this.queue.enqueue(task)
}

func (this *BasicScheduler) Tick() {
	task, ok := this.queue.dequeue()
	if !ok {
		return
	}

	if this.executor != nil && task != nil {
		this.executor.ExecuteTask(task)
	}
}

func (this *BasicScheduler) IsIdle() bool {
	return this.queue.isEmpty()
}
