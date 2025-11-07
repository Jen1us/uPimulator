# uPIMulator-CIMDi

## Docker
```bash
docker build --platform=linux/amd64 -t upim-chiplet docker/
```
```bash
docker run --platform=linux/amd64 --rm -it \
  -v /Users/Jenius/Projects/CIMDi/2025.09/Evaluation/uPIMulator:/root/uPIMulator \
  upim-chiplet /bin/bash
```
## Compile
```bash
python3 tools/generate_chiplet_model.py \
  --config config/Mixtral-8x7B/config.json \
  --output bin/mixtral_moe_chiplet.json \
  --seq-length 4096 --batch 1 \
  --digital-chiplets 4 --rram-chiplets 8 \
  --chunk-bytes 4194304

  python3 tools/generate_chiplet_model.py \
  --config config/Test/config.json \
  --output bin/Test_moe_chiplet.json \
  --seq-length 4096 --batch 1 \
  --digital-chiplets 4 --rram-chiplets 8 \
  --chunk-bytes 4194304
```
## Build
```bash
go build -o build/uPIMulator ./src
```
## Run
```bash
cd /root/uPIMulator/golang/uPIMulator && ./build/uPIMulator \
  --platform_mode chiplet \
  --benchmark TRANSFORMER \
  --root_dirpath /root/uPIMulator/golang/uPIMulator \
  --bin_dirpath /root/uPIMulator/golang/uPIMulator/bin \
  --chiplet_model_path /root/uPIMulator/golang/uPIMulator/bin/test_moe_chiplet.json \
  --chiplet_host_limit_resources 0 \
  --chiplet_host_stream_total_batches 0 \
  --chiplet_host_stream_low_watermark 1 \
  --chiplet_host_stream_high_watermark 2 \
  --chiplet_digital_activation_buffer 536870912 \
  --chiplet_digital_scratch_buffer 536870912 \
  --chiplet_rram_input_buffer 536870912 \
  --chiplet_rram_output_buffer 536870912 \
  --chiplet_digital_spus_per_chiplet 8 \
  --chiplet_progress_interval 100000 \
  --num_channels 1 --num_ranks_per_channel 1 --num_dpus_per_rank 1 \
  --num_tasklets 4 --chiplet_host_dma_ramulator_enabled 1 --chiplet_host_dma_ramulator_config ./golang/ramulator2/resources/hbm2_default.yaml \
  --chiplet_host_dma_bw 33554432 \
  --chiplet_noc_booksim_enabled 1 \
  --chiplet_noc_booksim_config /root/uPIMulator/golang/booksim2/runfiles/meshconfig \
  --chiplet_noc_booksim_binary /root/uPIMulator/golang/booksim2/src/booksim_service \
  --chiplet_noc_booksim_timeout_ms 5000 \
  --chiplet_stats_flush_interval 10000000 | tee run_debug.log 
```
# Ramulator2
## Build
```bash
cd /root/uPIMulator/golang/ramulator2 &&
rm -rf build && mkdir build && cd build &&
cmake -DCMAKE_POLICY_VERSION_MINIMUM=3.25 .. &&
cmake --build . -j$(nproc)
```
# BookSim2

## make
```bash
cd golang/booksim2/src && make booksim_service
```
## 独立测试
```bash
cd golang/booksim2/src
printf '{"op":"estimate","src":0,"dst":1,"bytes":64}\n{"op":"shutdown"}\n' \
  | ./booksim_service --config_file ../runfiles/meshconfig
```

注意：BookSim 节点编号需与 `chiplet` 拓扑一致（数字 Chiplet 节点 ID 从 0 开始，RRAM 节点依次排在数字节点之后），可在配置文件中通过 `topology = mesh` 与 `k = {NumDigital+NumRram}`、`n = 1`（2D mesh 示例）控制。
