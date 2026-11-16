#!/bin/sh
# Example elzar protocol-v1 exec plugin.
# Prerequisites: POSIX sh and jq >= 1.6 on PATH.

set -eu

if [ "${1:-}" = "describe" ]; then
	cat <<'EOF'
{
  "protocol": 1,
  "columns": [
    {
      "id": "workdir",
      "header": "WorkDir",
      "width": 24,
      "tabs": ["queue", "recent"]
    }
  ],
  "sections": [
    {
      "id": "paths",
      "title": "Job Paths (Plugin)"
    }
  ]
}
EOF
	exit 0
fi

input="$(cat)"
op="$(printf '%s' "$input" | jq -r '.op')"

case "$op" in
columns)
	printf '%s' "$input" | jq '{
		values: (
			reduce (.jobs // [])[] as $job (
				{};
				.[$job.JobID] = {
					workdir: ($job.WorkDir // "—")
				}
			)
		)
	}'
	;;
detail)
	printf '%s' "$input" | jq '{
		sections: {
			paths: [
				"WorkDir: " + (.job.WorkDir // "—"),
				"StdOut:  " + (.job.StdOut // "—"),
				"StdErr:  " + (.job.StdErr // "—")
			]
		}
	}'
	;;
*)
	printf 'unknown op: %s\n' "$op" >&2
	exit 1
	;;
esac
