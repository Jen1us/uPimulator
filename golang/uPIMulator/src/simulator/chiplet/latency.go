package chiplet

// TransferLatencyQuery 描述一次芯粒间传输的估算请求。
// 如果某个端点不适用，以 -1 表示。
type TransferLatencyQuery struct {
	Stage      string
	Bytes      int64
	SrcDigital int
	DstDigital int
	SrcRram    int
	DstRram    int
	Metadata   map[string]interface{}
}

// TransferLatencyEstimator 尝试返回更精确的传输延迟（周期）。
// 第二个返回值为 false 时表示估算失败，调用者应回退到带宽模型。
type TransferLatencyEstimator func(TransferLatencyQuery) (int, bool)
