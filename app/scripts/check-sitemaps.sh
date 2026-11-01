#!/bin/sh
# Validates generated sitemaps against the vendored XSDs (M2 acceptance). Needs xmllint and AFSC_TEST_DATABASE_URL;
# it fails rather than skips when either is missing.
set -eu
cd "$(dirname "$0")/.."

command -v xmllint >/dev/null || { echo "xmllint not found (libxml2-utils)" >&2; exit 1; }
[ -n "${AFSC_TEST_DATABASE_URL:-}" ] || { echo "AFSC_TEST_DATABASE_URL is not set" >&2; exit 1; }

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
AFSC_SITEMAP_DUMP="$out" go test -p 1 -count=1 -run '^(TestSitemaps|TestSitemapWriters)$' ./internal/seo/ ./internal/site/

for f in writer-urlset.xml writer-sitemap_index.xml site-sitemap_index.xml site-post-sitemap.xml site-post-sitemap2.xml \
	site-page-sitemap.xml site-category-sitemap.xml site-author-sitemap.xml site-video-sitemap.xml; do
	[ -s "$out/$f" ] || { echo "missing $f: did a test skip?" >&2; exit 1; }
done

status=0
for f in "$out"/*.xml; do
	case "$f" in
	*sitemap_index.xml) schema=testdata/xsd/siteindex.xsd ;;
	*) schema=testdata/xsd/urlset.xsd ;;
	esac
	xmllint --noout --nonet --schema "$schema" "$f" || status=1
done
exit $status
