#!/usr/bin/env bash
# Publish site/index.html to https://marionette.otimififi.site through the
# Otimififi API: write the page draft, commit it as a release, make it live.
# Needs OTIMIFIFI_API_BASE and OTIMIFIFI_ACCESS_TOKEN in the environment.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PAGE=284875b2-fc17-4fc5-a62a-75d67fc22495
TITLE="Pull Figma's strings from your AI agent"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# call METHOD PATH BODY_FILE — prints the response, fails unless status is success.
call() {
  local out
  out=$(curl -sS --noproxy '*' -X "$1" \
    -H "Authorization: Bearer $OTIMIFIFI_ACCESS_TOKEN" -H 'Content-Type: application/json' \
    --data-binary "@$3" "$OTIMIFIFI_API_BASE/api/v1/$2")
  python3 -c 'import json, sys; d = json.loads(sys.argv[1]); d.get("status") == "success" or sys.exit("otimififi: %s" % d.get("message"))' "$out"
  printf '%s' "$out"
}

python3 -c 'import json, sys; print(json.dumps({"html": open(sys.argv[1]).read(), "import_mode": "full_html", "title": sys.argv[2]}))' \
  "$HERE/index.html" "$TITLE" > "$TMP/draft.json"
echo '{}' > "$TMP/empty.json"

call PUT "pages/$PAGE/drafts/0" "$TMP/draft.json" >/dev/null
version=$(call POST "pages/$PAGE/drafts/0/commit" "$TMP/empty.json" | python3 -c 'import json, sys; print(json.load(sys.stdin)["payload"]["version"])')
echo "{\"version\": $version}" > "$TMP/live.json"
call PUT "pages/$PAGE/change-version" "$TMP/live.json" >/dev/null

echo "published version $version -> https://marionette.otimififi.site"
