# Running on AWS

Two hosts in one cluster placement group: the system under test (SUT) on local NVMe, and a separate load generator, so the generator never takes CPU from the ledger.

| Host | Type | Why |
|---|---|---|
| sut | `i4i.2xlarge` (8 vCPU, 64 GiB, local NVMe) | Postgres and TigerBeetle are bound by `fsync`; EBS would dominate both |
| loadgen | `c7i.2xlarge` | enough CPU to drive both systems |

Check current on-demand prices before starting; a day of runs should be in the tens of dollars. Terminate both hosts when done, because instance-store data is lost on stop anyway.

## 1. Launch (Ubuntu 24.04, default VPC)
```sh
AMI=$(aws ssm get-parameter --name /aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id --query Parameter.Value --output text)
aws ec2 create-placement-group --group-name torbench --strategy cluster
SG=$(aws ec2 create-security-group --group-name torbench --description torbench --query GroupId --output text)
aws ec2 authorize-security-group-ingress --group-id $SG --protocol tcp --port 22 --cidr $(curl -s https://checkip.amazonaws.com)/32
aws ec2 authorize-security-group-ingress --group-id $SG --protocol all --source-group $SG
for t in sut:i4i.2xlarge loadgen:c7i.2xlarge; do
  aws ec2 run-instances --image-id $AMI --instance-type ${t#*:} --key-name <your-key> \
    --security-group-ids $SG --placement GroupName=torbench \
    --tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=torbench-${t%%:*}}]"
done
```

## 2. Prepare both hosts
Copy both repos next to each other on each host, then run the setup script.
```sh
rsync -a --exclude results --exclude bin TorBench Torment ubuntu@<host>:
ssh ubuntu@<sut>     'sudo TorBench/scripts/host-setup.sh sut'
ssh ubuntu@<loadgen> 'sudo TorBench/scripts/host-setup.sh loadgen'
```
The loadgen host needs SSH access to the SUT (the matrix resets the stack between systems): add an SSH key from the loadgen host to the SUT's `authorized_keys`.

## 3. Run the matrix from the loadgen host
```sh
cd TorBench
export SUT_SSH=ubuntu@<sut-private-ip> OUT=results/aws
URL=http://<sut-private-ip>:8088 scripts/matrix.sh torment
URL=http://<sut-private-ip>:3068 scripts/matrix.sh formance default
URL=http://<sut-private-ip>:3068 scripts/matrix.sh formance minimal
make report   # writes results/report.html
```
Each matrix is 3 contention levels × 8 steps of 5 minutes, about 2 hours per system with seeding and checks. Run inside `tmux` so it survives a dropped SSH session. To repeat a matrix for medians, run it again with a different `OUT`.

CPU and memory on the SUT while a matrix runs:
```sh
while sleep 5; do docker stats --no-stream --format '{{json .}}'; done >> stats.jsonl
```
