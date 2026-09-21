# Environment C: AWS EC2 Phase 8 runbook

This runbook prepares a second host for the portable Phase 8 reproduction
suite. It does **not** change the benchmark, tune io_uring, or provision
anything automatically. Execute the commands deliberately, preserving all
output and stopping at each gate described below.

The intended shape is an AWS `c8id.2xlarge` running official Ubuntu Server
24.04 LTS on x86-64 (8 vCPU, 16 GiB RAM). The operating system may use an
ordinary gp3 EBS root volume. The checkout and experimental file must instead
be on that instance type's local instance-store NVMe, formatted as ext4.

Instance-store data is ephemeral: stopping or terminating the instance loses
the local NVMe contents. Copy the completed artifact archive off the instance
before stopping or terminating it.

This reproduction suite is intended to support or falsify the portability of
the mechanism evidence before an upstream `golang/go` discussion.

## 1. Local AWS choices and AMI resolution

Install and authenticate the AWS CLI locally using credentials authorized to
create the resources below. Do not put credentials in this repository. Choose
the region explicitly; `us-east-1` is a practical initial choice from
Argentina only for availability/cost purposes, not benchmark latency.

```bash
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION="$AWS_REGION"
export INSTANCE_TYPE=c8id.2xlarge
export UBUNTU_SSM_PARAMETER=/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id

aws sts get-caller-identity
AMI_ID=$(aws ssm get-parameter \
  --region "$AWS_REGION" \
  --name "$UBUNTU_SSM_PARAMETER" \
  --query 'Parameter.Value' --output text)
printf 'AMI_ID=%s\n' "$AMI_ID"
aws ec2 describe-images --region "$AWS_REGION" --image-ids "$AMI_ID" \
  --query 'Images[0].{ImageId:ImageId,Name:Name,Architecture:Architecture,State:State,OwnerId:OwnerId}' \
  --output table
```

The SSM public parameter resolves a current Canonical Ubuntu Server 24.04 LTS
amd64 EBS/gp3 AMI in the selected region. Do not hard-code an AMI ID. Stop if
the returned image is not available or is not `x86_64`/`available`.

Select a subnet in an availability zone where `c8id.2xlarge` is offered. Check
availability before creating a security group or launching:

```bash
export SUBNET_ID=subnet-REPLACE_ME
export AZ=$(aws ec2 describe-subnets --region "$AWS_REGION" --subnet-ids "$SUBNET_ID" \
  --query 'Subnets[0].AvailabilityZone' --output text)
aws ec2 describe-instance-type-offerings --region "$AWS_REGION" \
  --location-type availability-zone --filters "Name=instance-type,Values=$INSTANCE_TYPE" \
  --query 'InstanceTypeOfferings[].{InstanceType:InstanceType,AvailabilityZone:Location}' \
  --output table
```

Confirm that the table includes `$AZ`. If that instance type is unavailable,
choose another region/AZ explicitly and record the choice. Do not silently
substitute a different instance type.

## 2. SSH-only security group and key pair

Only SSH is needed. Determine the operator's public IPv4 address and use a
single-host `/32` CIDR; inspect it before using it. Never authorize SSH from
`0.0.0.0/0` and do not open any additional inbound ports.

```bash
export VPC_ID=vpc-REPLACE_ME
export MY_CIDR=REPLACE_WITH_YOUR_PUBLIC_IPV4/32
export SG_NAME=phase8-ssh-$(date +%Y%m%d-%H%M%S)

SG_ID=$(aws ec2 create-security-group --region "$AWS_REGION" \
  --group-name "$SG_NAME" --description 'Phase 8 temporary SSH only' \
  --vpc-id "$VPC_ID" --query GroupId --output text)
aws ec2 authorize-security-group-ingress --region "$AWS_REGION" \
  --group-id "$SG_ID" --ip-permissions \
  "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=$MY_CIDR,Description=phase8-operator}]"
aws ec2 describe-security-groups --region "$AWS_REGION" --group-ids "$SG_ID" --output table
```

Use an existing key pair when possible:

```bash
export KEY_NAME=existing-key-pair-name
aws ec2 describe-key-pairs --region "$AWS_REGION" --key-names "$KEY_NAME"
```

If a temporary key pair is required, protect its locally written private key
and record that it must be removed during cleanup:

```bash
export KEY_NAME=phase8-key-$(date +%Y%m%d-%H%M%S)
export KEY_FILE="$HOME/.ssh/$KEY_NAME.pem"
umask 077
aws ec2 create-key-pair --region "$AWS_REGION" --key-name "$KEY_NAME" \
  --query KeyMaterial --output text >"$KEY_FILE"
chmod 600 "$KEY_FILE"
```

## 3. Launch and record resource identity

Use a normal gp3 root volume for Ubuntu. Do not attach EBS storage for the
benchmark. Instance-store NVMe is supplied by the `c8id` instance type and is
not an EBS volume mapping.

```bash
INSTANCE_ID=$(aws ec2 run-instances --region "$AWS_REGION" \
  --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" \
  --subnet-id "$SUBNET_ID" --security-group-ids "$SG_ID" \
  --key-name "$KEY_NAME" --block-device-mappings \
  'DeviceName=/dev/sda1,Ebs={VolumeSize=30,VolumeType=gp3,DeleteOnTermination=true}' \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=phase8-environment-c}]' \
  --query 'Instances[0].InstanceId' --output text)
printf 'start_utc=%s\nregion=%s\ninstance_id=%s\ninstance_type=%s\n' \
  "$(date -u +%FT%TZ)" "$AWS_REGION" "$INSTANCE_ID" "$INSTANCE_TYPE" | tee phase8-aws-session.txt

aws ec2 wait instance-running --region "$AWS_REGION" --instance-ids "$INSTANCE_ID"
aws ec2 describe-instances --region "$AWS_REGION" --instance-ids "$INSTANCE_ID" \
  --query 'Reservations[0].Instances[0].{State:State.Name,PublicIp:PublicIpAddress,PrivateIp:PrivateIpAddress,AZ:Placement.AvailabilityZone,RootDevice:RootDeviceName,BlockDevices:BlockDeviceMappings}' \
  --output json | tee -a phase8-aws-session.txt
```

Obtain the public IP only after the instance is running, then connect as
Ubuntu's default user:

```bash
PUBLIC_IP=$(aws ec2 describe-instances --region "$AWS_REGION" --instance-ids "$INSTANCE_ID" \
  --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)
ssh -i "$KEY_FILE" ubuntu@"$PUBLIC_IP"
```

If using an existing SSH configuration/key, adjust only the SSH command. Do
not relax the security group as a workaround for connectivity.

## 4. Bootstrap only the needed host tools

On the instance, record the initially installed tools. The production Go
experiment uses direct Go syscalls; it does not need cgo or `liburing`.
Phase 8's mandatory Go race-detector gate on linux/amd64 does require cgo, a
C compiler, and libc development headers. Install `gcc` and `libc6-dev` for
that gate; `build-essential` and liburing remain unnecessary.

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git python3 util-linux nvme-cli gcc libc6-dev linux-tools-common "linux-tools-$(uname -r)"
command -v curl git python3 findmnt lsblk sha256sum nvme
perf --version || true
```

If Ubuntu cannot provide `linux-tools-$(uname -r)`, retain that concrete
failure in the final artifact/environment record; Phase 8 does not require
`perf` to run. Do not install liburing.

## 5. Mandatory instance-store identification gate

The Linux `/dev/nvme*n1` name is not stable and must never be assumed. Before
formatting, inspect both the block layout and NVMe controller identification:

```bash
lsblk -o NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,MODEL,SERIAL
sudo nvme list
for d in /dev/nvme*n1; do
  [ -b "$d" ] || continue
  echo "===== $d ====="
  sudo nvme id-ctrl -v "$d" | grep -E '^(mn|sn|vid|ssvid)' || true
done
findmnt -no SOURCE,TARGET,FSTYPE /
```

On Nitro instances, root EBS is normally identifiable as an NVMe controller
whose model is `Amazon Elastic Block Store` and whose serial corresponds to an
EBS volume ID (often without the dash). Instance-store NVMe normally reports
`Amazon EC2 NVMe Instance Storage`. Treat these as evidence, not a substitute
for operator review. Cross-check the root source from `findmnt /`, the EC2
console/API block-device mappings, model, serial, and capacity.

**Stop here unless the operator has positively identified the local
instance-store device.** Never format the root EBS device or an uncertain
device. Set the following value manually only after that review:

```bash
export INSTANCE_STORE_DEVICE=/dev/nvmeREPLACE_ME
```

## 6. Format and mount the disposable ext4 benchmark filesystem

Formatting is intentionally an explicit privileged/destructive operator step
after the identity gate. It destroys the selected instance-store contents.

```bash
sudo mkfs.ext4 "$INSTANCE_STORE_DEVICE"
sudo mkdir -p /mnt/phase8
sudo mount "$INSTANCE_STORE_DEVICE" /mnt/phase8
sudo chown ubuntu:ubuntu /mnt/phase8

findmnt /mnt/phase8
mount | grep -F ' /mnt/phase8 '
df -T /mnt/phase8
lsblk -o NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,ROTA,MODEL,SERIAL
```

Do not add `/etc/fstab` entries: the host is disposable and instance-store is
ephemeral. The checkout and file must be below this mount:

```text
/mnt/phase8/bench
/mnt/phase8/bench/data/readat-512MiB.bin
```

## 7. Install and verify official Go 1.27.1

Do not replace Ubuntu's system Go or use it if it is a different release.
Download the official linux/amd64 archive and independently obtain its
published SHA-256 from Go's release metadata before extraction. This command
uses Python's standard library; inspect its printed URL and hash.

```bash
cd /tmp
python3 - <<'PY'
import json, urllib.request
want = 'go1.27.1.linux-amd64.tar.gz'
for release in json.load(urllib.request.urlopen('https://go.dev/dl/?mode=json&include=all')):
    for f in release.get('files', []):
        if f.get('filename') == want:
            print(f"{f['filename']} {f['sha256']} https://go.dev/dl/{f['filename']}")
            raise SystemExit
raise SystemExit('official Go 1.27.1 linux/amd64 metadata not found')
PY
```

Copy the displayed SHA-256 and URL into the following commands; do not invent
or trust a hash from memory:

```bash
export GO_ARCHIVE=go1.27.1.linux-amd64.tar.gz
export GO_URL=https://go.dev/dl/$GO_ARCHIVE
export GO_SHA256=PASTE_THE_METADATA_SHA256_HERE
curl -fL --proto '=https' --tlsv1.2 -o "$GO_ARCHIVE" "$GO_URL"
printf '%s  %s\n' "$GO_SHA256" "$GO_ARCHIVE" | sha256sum -c -
sudo rm -rf /opt/go1.27.1
sudo mkdir -p /opt/go1.27.1
sudo tar -C /opt/go1.27.1 --strip-components=1 -xzf "$GO_ARCHIVE"
export GO_TOOL=/opt/go1.27.1/bin/go
"$GO_TOOL" version
readlink -f "$GO_TOOL"
"$GO_TOOL" env CGO_ENABLED CC
command -v gcc
```

The `sudo rm -rf /opt/go1.27.1` command is only appropriate when replacing a
previous Phase 8 installation in that exact path. On a first install omit it.
Record the archive SHA-256 and resolved tool path in the execution notes; the
Phase 8 runner also records the resolved tool and version.

## 8. Checkout or transfer the exact investigation tree

Use either a clone reachable to the operator or a source archive copied from
Environment B. Do not transfer historical Phase 2--7 result archives unless
they are separately needed later for comparison. The clean checkout itself
must land on instance-store:

```bash
cd /mnt/phase8
git clone REPOSITORY_URL bench
cd bench
git fetch --all --tags
export CHECKOUT_REVISION=REPLACE_WITH_REVIEWED_COMMIT_CONTAINING_GATE_FIXES
git checkout --detach "$CHECKOUT_REVISION"
git merge-base --is-ancestor dfc28d1823dcdb577194cf0b4091ff6cdbd98d57 HEAD
git rev-parse HEAD
git status --short
```

For an archive transfer, use a Git bundle or a working-tree archive that
preserves `.git`, extract it as `/mnt/phase8/bench`, then make the same `git
merge-base --is-ancestor` and status checks. A plain source-only tarball
cannot establish the required commit ancestry and is not an acceptable
substitute. A known descendant is allowed, but record the actual `HEAD`.
Stop if the ancestry check fails or tracked source is dirty. The data file
remains untracked under the ignored `data/` directory.

Use the reviewed revision containing both the deterministic batching-test
fix and the explicit race prerequisite. `dfc28d1823dcdb577194cf0b4091ff6cdbd98d57`
remains the implementation ancestry requirement; checking out that older
commit itself would omit these gate corrections.

## 9. Manual preflight and stop conditions

Before the runner, capture the following in a terminal log or a file copied
with the artifacts:

```bash
cd /mnt/phase8/bench
uname -a
lscpu
nproc
free -h
lsblk -o NAME,TYPE,SIZE,FSTYPE,MOUNTPOINTS,ROTA,MODEL,SERIAL
findmnt /mnt/phase8
findmnt -T ./data/readat-512MiB.bin || true
cat /proc/sys/kernel/io_uring_disabled
grep -E '^(NoNewPrivs|Seccomp|Seccomp_filters):' /proc/self/status
"$GO_TOOL" version
```

Confirm before proceeding:

- `nproc` is 8 as expected for `c8id.2xlarge`; otherwise record the mismatch
  and stop to confirm the instance type.
- available memory is approximately 16 GiB;
- the checkout and dataset resolve to ext4 on `/mnt/phase8`, not the EBS root;
- `io_uring_disabled` and the process security state do not indicate an
  obvious restriction. The runner's actual setup probe, not these fields,
  decides availability.

Do not alter io_uring sysctls, security policy, CPU governor, scheduling
configuration, mount options, or run other CPU-intensive work concurrently.

## 10. Execute the canonical Phase 8 suite

The runner builds/generates the experiment-owned 512 MiB input only when it
does not exist; it never overwrites an existing data file. It chooses Part A
`GOMAXPROCS=min(4, online CPUs)` (therefore 4 here) and Part B
`min(8, online CPUs)` (therefore 8 here). Do not set Phase 8 GOMAXPROCS
overrides for the first run.

```bash
cd /mnt/phase8/bench
GO_TOOL=/opt/go1.27.1/bin/go \
  scripts/phase8-cross-host-reproduction.sh \
  phase8-artifacts \
  ./data/readat-512MiB.bin
```

The gate runs tests, a real `io_uring_setup` probe, and a smoke before the
matrix. Before those tests it compiles a small cgo package under `-race` with
`CGO_ENABLED=1`; this verifies the race-detector prerequisite and writes
`race-preflight.txt`. The benchmark binaries themselves are built with their
ordinary settings. If setup is unavailable or any check fails, stop immediately. Preserve
the partial `phase8-artifacts/` directory and diagnose from its concrete
`setup-probe.json`, test logs, run JSON, stderr, and check output. Do not
manually retry an individual matrix point or replace a failed JSON file.

After a successful run, verify raw artifacts first:

```bash
find phase8-artifacts -maxdepth 2 -type f | sort
sed -n '1,240p' phase8-artifacts/environment.txt
sed -n '1,240p' phase8-artifacts/capabilities.txt
cat phase8-artifacts/dataset.txt
```

## 11. Post-matrix worker identity observations

Only after the complete canonical matrix succeeds, run the four separate
diagnostic observations below. They are not throughput repetitions. Preserve
their sample counts and do not claim quantitative worker identity conclusions
from an inadequate (<100 samples in five seconds) observation.

`phase4-observe-workers.sh` is an existing identity diagnostic with its own
fixed `GOMAXPROCS=4`; it is intentionally not a Phase 8 throughput point. The
exported `GO_TOOL` below avoids its legacy local-path fallback.

```bash
cd /mnt/phase8/bench
export GO_TOOL=/opt/go1.27.1/bin/go
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U1 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=1
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U4 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=4
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-U16 ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread --unbounded-workers=16
scripts/phase4-observe-workers.sh uring phase8-artifacts/observe-default ./data/readat-512MiB.bin \
  --prewarm --force-async --lock-submitter-thread
```

## 12. Package, copy off-instance, then clean up

Package the complete directory on the instance-store, record the exact source
revision and archive hash, and copy the archive to a durable local machine or
other operator-controlled storage **before** stopping/terminating the EC2
instance:

```bash
cd /mnt/phase8/bench
git rev-parse HEAD | tee phase8-artifacts/execution-head.txt
tar -C . -czf phase8-artifacts.tar.gz phase8-artifacts
sha256sum phase8-artifacts.tar.gz | tee phase8-artifacts.tar.gz.sha256
```

For example, from the operator workstation (use a destination outside the EC2
instance-store):

```bash
scp -i "$KEY_FILE" ubuntu@"$PUBLIC_IP":/mnt/phase8/bench/phase8-artifacts.tar.gz .
scp -i "$KEY_FILE" ubuntu@"$PUBLIC_IP":/mnt/phase8/bench/phase8-artifacts.tar.gz.sha256 .
sha256sum -c phase8-artifacts.tar.gz.sha256
```

Do not require Environment B Phase 7 artifacts on Environment C. Run
`cmd/phase8compare` later on a suitable machine after copying both archives.

After confirming the copied archive checksum and recording the start/end time,
instance ID, type, and region, terminate the disposable instance. Then verify
that separately billable resources are gone:

```bash
aws ec2 terminate-instances --region "$AWS_REGION" --instance-ids "$INSTANCE_ID"
aws ec2 wait instance-terminated --region "$AWS_REGION" --instance-ids "$INSTANCE_ID"

# Inspect first. The root volume was requested with DeleteOnTermination=true,
# but do not assume every separately created volume disappeared.
aws ec2 describe-volumes --region "$AWS_REGION" \
  --filters "Name=attachment.instance-id,Values=$INSTANCE_ID" --output table
aws ec2 describe-addresses --region "$AWS_REGION" \
  --filters "Name=instance-id,Values=$INSTANCE_ID" --output table

# Delete only the temporary resources created for this run.
aws ec2 delete-security-group --region "$AWS_REGION" --group-id "$SG_ID"
# If and only if this run created the key pair:
aws ec2 delete-key-pair --region "$AWS_REGION" --key-name "$KEY_NAME"
```

Remove the corresponding temporary local private-key file only after no longer
needed for artifact transfer. Also check the EC2 console/API for unattached
volumes, snapshots, Elastic IPs, and security groups created by this run. Do
not assume instance termination removes every independently created AWS
resource.

## Failure conditions summary

Stop rather than working around any of the following:

- no `c8id.2xlarge` offering in the selected AZ;
- wrong AMI architecture/state;
- security group would require broad SSH exposure;
- uncertain root versus instance-store device identity;
- dataset/check-out not on the ext4 instance-store mount;
- Go archive hash mismatch or wrong `go version`;
- required Phase 8 commit is not an ancestor or source is tracked-dirty;
- the cgo/race preflight, `io_uring_setup`, tests/smoke, or residency acceptance fails,
  or a matrix run/check fails;
- artifact archive cannot be copied and checksum-verified before cleanup.

These are experimental stop conditions, not prompts to weaken security,
change host configuration, or alter the benchmark matrix.
