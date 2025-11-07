package chiplet

// HostTaskStager buffers tasks produced by the host runtime before they are
// submitted to the scheduler. It allows future work to implement batching,
// dependency analysis, or reordering policies without changing the platform
// loop.
type HostTaskStager struct {
	queue taskQueue
}

func (this *HostTaskStager) Init() {
	this.queue = taskQueue{items: make([]*Task, 0)}
}

func (this *HostTaskStager) Fini() {
	this.queue.items = nil
}

func (this *HostTaskStager) Enqueue(task *Task) {
	if task == nil {
		return
	}

	this.queue.enqueue(task)
}

func (this *HostTaskStager) HasPending() bool {
	return !this.queue.isEmpty()
}

func (this *HostTaskStager) Pop() (*Task, bool) {
	return this.queue.dequeue()
}
