# TorBench

Benchmarks [Torment](../Torment) against Formance ledger v2 on a simulated stock brokerage, checking every run for correctness. Design and fairness choices are in [docs/decisions.md](docs/decisions.md); real runs go on AWS, see [docs/aws.md](docs/aws.md).

Needs Docker, Go and jq, and the Torment repo next to this one (`../Torment`, or set `TORMENT_DIR`).

```sh
make fresh SYSTEM=torment            # wipe volumes, start one system (torment or formance)
make seed  SYSTEM=torment            # register and fund 10k users, 20 tickers
make run   SYSTEM=torment ARGS="-concurrency 64 -duration 1m"
make run   SYSTEM=formance FEATURES=minimal ARGS="-rate 200 -shards 16"
scripts/matrix.sh torment            # every step for one system; DURATION=30s for a quick pass
make report                          # results/report.html from every result file
make test
```

`go run ./cmd/torbench run -h` lists every flag. Laptop numbers only compare the two systems roughly; Docker on macOS adds a VM between both databases and the disk.
