# AWS EC2 benchmark runbook — AMD64 reference platform

The paper's AMD64 reference platform is **AWS EC2 `c8i.4xlarge`** (Intel
Xeon 6 / Granite Rapids, 16 vCPU). This directory holds the bootstrap
script for that instance.

## Files

- [`c8i-userdata.sh`](c8i-userdata.sh) — cloud-init user-data. Installs
  build tools, Go 1.25, OpenJPH 0.15.0, CharLS 2.4.2, clones the repo,
  and runs the preflight build.

## Launching the instance

1. **Console → EC2 → Launch instance.**
2. **AMI:** Canonical Ubuntu 24.04 LTS, AMD64, HVM, EBS-backed. Easiest
   way to get the current AMI ID is the SSM parameter
   `/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id`
   (or filter the AMI catalog for "Ubuntu Server 24.04 LTS (HVM), SSD
   Volume Type", owner `099720109477`).
3. **Instance type:** `c8i.4xlarge`.
4. **Storage:** default 30 GB gp3 is plenty for the build outputs; the
   benchmarks themselves are CPU-bound.
5. **Network:** assign a public IP if you want SSH; otherwise enable SSM
   Session Manager (attach `AmazonSSMManagedInstanceCore` to the
   instance role).
6. **Advanced details → User data:** paste the contents of
   `c8i-userdata.sh`. Override the repo URL by prepending
   `MIC_REPO_URL=...` if you clone from a fork.
7. **Launch.**

Bootstrap runs as root on first boot and takes ~10–15 minutes (mostly
CharLS/OpenJPH compilation). When `/home/ubuntu/BOOTSTRAP_DONE` exists
the machine is ready; bootstrap log is at `/var/log/mic-bootstrap.log`.

## Running the benchmarks

```bash
ssh ubuntu@<public-ip>            # or: aws ssm start-session ...
cd ~/medical-image-codec
./run-paper-benchmarks.sh         # paper default: -benchtime=10x
```

Results land in `results/<timestamp>/`. Pull them back with:

```bash
scp -r ubuntu@<public-ip>:~/medical-image-codec/results/<timestamp> ./results-c8i/
```

## What this script deliberately does *not* touch

- **CPU governor / turbo / SMT.** The prior AMD64 reference left these at
  vendor defaults; this script does too, so the platform change is the
  only variable.
- **Benchmark execution.** Bootstrap stops at the preflight build. Run
  the benchmark script interactively so variance and any
  thermal/noisy-neighbour behaviour stay visible.

## Stopping the instance

`c8i.4xlarge` is around \$0.85/hr on-demand in `us-east-1`. The benchmark
run itself is under an hour; remember to stop or terminate after pulling
results back.
