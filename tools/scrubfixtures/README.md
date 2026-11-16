# Fixture scrubber

Generate repository fixtures from raw Slurm JSON dumps with:

```sh
go run ./tools/scrubfixtures -input /private/path/squeue_all.json -output testdata/squeue.json -max-jobs 20
```

The scrubber puts pending jobs first when trimming, then keeps the original
order within each state group. `-offset` and `-count` can split a selected job
set into multiple compact JSON fixtures; scrub the same full set for each
piece so anonymized values remain consistent. Use `-omit-job-resources` for
queue fixtures that do not need the large allocation details.

It replaces user and group names and IDs, accounts, work keys, job names, site
labels, node names, paths, scripts, command lines, comments, and free-form
extra text with deterministic fixture values. Keep raw dumps outside the
repository; only commit the scrubber's output.

For account samples, put the labelled command captures (`*.out`) and the
multi-cluster capture named `sshare-all.out` in one private directory:

```sh
go run ./tools/scrubfixtures -input-dir /private/samples -output-dir testdata/accounts
```

Files are processed in sorted order with one shared identity map. Output filenames
use sequential `site-01`, `site-02`, etc. labels instead of input site names. The tool
splits command-labelled documents and preserves anonymized `CLUSTER:` headers.
It retains the first few share rows, user rows, and their ancestor accounts,
while omitting large share TRES arrays and QOS descriptions. Association and
QOS references use the same names across files. Partition captures retain access lists, state, and time/node limits; topology
and unrelated fields are omitted. Standalone partition JSON uses `-input`/`-output`. Do not include raw samples in git.
