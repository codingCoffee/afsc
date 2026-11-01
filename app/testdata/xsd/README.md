# Sitemap schemas

Vendored copies, used by `scripts/check-sitemaps.sh` (fetched 2026-11-01):

| File | Source |
|---|---|
| `sitemap.xsd` | https://www.sitemaps.org/schemas/sitemap/0.9/sitemap.xsd |
| `siteindex.xsd` | https://www.sitemaps.org/schemas/sitemap/0.9/siteindex.xsd |
| `sitemap-image.xsd` | https://www.google.com/schemas/sitemap-image/1.1/sitemap-image.xsd |
| `sitemap-video.xsd` | https://www.google.com/schemas/sitemap-video/1.1/sitemap-video.xsd |

`urlset.xsd` is ours: it imports the three urlset schemas so one `xmllint --schema` call checks the image and video
elements too (the sitemap schema validates other namespaces strictly).
