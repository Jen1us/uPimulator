# Ramulator 接入 Chiplet Host DMA 设计稿

## 1. 目标

将 Chiplet 模式中 Host ↔ Digital 之间的 DMA 传输时序交给 Ramulator 做精确建模。当前 `host.DMAController` 通过简单的带宽估算得到传输延迟；引入 Ramulator 后，可依据 Bank/Rank/Channel 队列状态得到更真实的 service time，从而影响 Chiplet 平台的互联与流水调度。

> **范围限定**：首阶段仅覆盖 Host→Digital 及 Digital→Host 的 DMA（KV cache/load、结果写回等）；Digital↔RRAM 仍沿用专用互联模型。后续若需要，可再扩展到 RRAM/DRAM 混合场景。

## 2. 当前数据流（概览）

1. `chiplet_model` 生成的命令中包含 `transfer_host2d` / `transfer_d2host`，由 `HostOrchestrator` 发射到 Chiplet 平台。
2. `ChipletPlatform.handleTransferTask()` 根据命令方向调用 `host.DMAController.RecordLoad/Store`，累积统计并设置固定延迟。
3. 传输完成后写入 `cycle_log` / `log_dump`，影响 Host backpressure 与 Streaming 调度。

## 3. 集成策略

### 3.1 需要的接口

- **从 Chiplet 到 Ramulator**：
  - 请求结构：`{bytes, source_id, dest_id, issue_cycle, metadata}`。
  - API：同步（阻塞）或异步（查询完成）两种选型，第一阶段采用同步调用——即 Chiplet 发起一次 DMA，Ramulator 返回预计完成周期。
- **从 Ramulator 到 Chiplet**：
  - 主要返回 `service_time`（单位 cycle），后续可扩展为功耗/排队信息。

### 3.2 代码接入点

| 模块 | 现状 | 修改内容 |
| --- | --- | --- |
| `host/dma.go` (`DMAController`) | 仅计算/统计字节数 | 添加 `ramulatorClient` 字段，调用 `EstimateCycles(bytes, metadata)` 获取延迟 |
| `chiplet_platform.go` | 直接使用固定带宽估算 | 改为调用 `DMAController.ScheduleTransfer(...)`，获取完成绝对 cycle |
| `HostOrchestrator` | 不感知真实延迟 | 保持不变，但需处理 DMA 返回的 `completeCycle` 以便正确 backpressure |

### 3.3 Ramulator 运行方式

建议先通过 **独立进程 + JSON RPC** 验证：
1. 启动 Ramulator，加载配置（HBM/DDR 等）。
2. Chiplet 通过 socket 将 DMA 请求（大小、地址、时间戳）发给 Ramulator。
3. Ramulator 返回完成延迟；Chiplet 在本地事件队列中注册完成。

若性能瓶颈明显，再考虑 CGO/FFI 将 Ramulator 嵌入到同一进程。

## 4. 待办列表

1. **基础封装**
   - 定义 `host/ramulator` package（或 `host/interconnect`）用于与 Ramulator 通信；
   - 提供 `Init(configPath string)`、`Estimate(bytes int64, metadata map[string]interface{}) (cycles int)`。
2. **DMAController 改造**
   - 新增配置项：`chiplet_host_dma_ramulator_enabled`、`chiplet_host_dma_ramulator_config`;
   - `ScheduleLoad/Store` 调用 Ramulator（若开关打开），否则回退带宽模型。
3. **ChipletPlatform 延迟处理**
   - 传输任务插入 `hostDmaPending` 映射，直到 Ramulator 返回的完成周期为止；
   - 更新 `cycle_log` 中的 `host_dma_*` 字段，使用真实延迟。
4. **工具链**
   - 在 `docs/perf_analysis.md` 中注明 Ramulator 支持；
   - 增加回归脚本参数检查 Ramulator 是否开启（比如 `chiplet_regression.py --require-host-dma`）。
5. **验证步骤**
   - 写一段 stub（Ramulator 返回固定延迟），先保证 pipeline 正常；
   - 再与真实 Ramulator 进程对接，对比统计数据是否变化；
   - 测量 `run_debug.log` 中 `[chiplet-debug]` 的传输时序是否反映新的延迟。

## 5. 后续扩展

- 支持 Ramulator 批量请求（合并多个 DMA），减少 IPC 压力；
- 结合 BookSim，将 Ramulator 反馈的完成周期与 NoC 模拟结果相加；
- 为 `log_dump` 增添 DRAM/PIM 能耗统计字段。

## 6. 本地服务落地（2025-11 更新）

- 在 `golang/ramulator2/src/service` 新增 `ramulator2_service`，通过 STDIN/STDOUT 传递 JSON 命令：
  - `{"op":"estimate","bytes":...,"access":"read|write","burst_bytes":64}` → `{"ok":true,"cycles":...}`；
  - `{"op":"ping"}` / `{"op":"shutdown"}` 用于探活与退出。
- 构建方式：
  ```bash
  cd golang/ramulator2
  rm -rf build && mkdir build && cd build
  cmake -DCMAKE_POLICY_VERSION_MINIMUM=3.25 ..
  cmake --build . -j
  ```
  可执行文件默认生成在 `golang/ramulator2/build/ramulator2_service`。
- Go 侧 `host/ramulator.Client` 会自动探测常见路径，亦可通过环境变量 `UPIMULATOR_RAMULATOR_SERVICE` 指定服务位置。
- 典型 HBM2/RoBaRaCoCh 的参考配置已写在 `golang/ramulator2/resources/hbm2_default.yaml`（8 channel × 2 rank，2 Gbps preset，Generic controller/FRFCFS/ClosedRowPolicy）；若自定义芯粒参数差异不大，可直接复用。
- uPIMulator 自动生成的请求会填充 `metadata`，无需额外 trace；Ramulator 的 YAML 只需描述 DRAM/控制器/前端骨架。服务启动时若报错会原样透传至 uPIMulator 日志，随后回退带宽模型。

---

如需进一步拆分任务，可将“Ramulator 客户端实现”“DMAController 接入”“回归/验证”分别进入独立分支迭代。


cd /root/uPIMulator/golang/ramulator2 &&
rm -rf build && mkdir build && cd build &&
cmake -DCMAKE_POLICY_VERSION_MINIMUM=3.25 .. &&
cmake --build . -j$(nproc)