package site

import (
  "cmp"
  "context"
  "html/template"
  "slices"

  "afsc/internal/content"
  "afsc/internal/theme"
)

type hydrateOpts struct {
  teaser bool // set Teaser, TeaserText and HasMore (listings and feeds)
  feed   bool // replace ContentHTML with feed-safe markup
  render bool // replace ContentHTML with a fresh render (dev mode, so partial edits show at once)
}

// hydrate fills authors, terms, featured media, URLs, ancestors and render-time HTML of loaded posts.
func (st *Site) hydrate(ctx context.Context, snap *snapshot, rows []*postRow, o hydrateOpts) error {
  if len(rows) == 0 {
    return nil
  }
  byID := map[int64]*postRow{}
  ids := make([]int64, 0, len(rows))
  for _, p := range rows {
    byID[p.ref.ID] = p
    ids = append(ids, p.ref.ID)
  }

  ar, err := st.db.Query(ctx, sqlPostAuthors, ids)
  if err != nil {
    return err
  }
  for ar.Next() {
    var postID, authorID int64
    if err := ar.Scan(&postID, &authorID); err != nil {
      ar.Close()
      return err
    }
    if a := snap.authorView(authorID); a != nil {
      byID[postID].view.Authors = append(byID[postID].view.Authors, a)
    }
  }
  if err := ar.Err(); err != nil {
    return err
  }

  tr, err := st.db.Query(ctx, sqlPostTerms, ids)
  if err != nil {
    return err
  }
  for tr.Next() {
    var postID, termID int64
    if err := tr.Scan(&postID, &termID); err != nil {
      tr.Close()
      return err
    }
    t := snap.termView(termID)
    if t == nil {
      continue
    }
    v := byID[postID].view
    if t.Taxonomy == "category" {
      v.Categories = append(v.Categories, t)
    } else {
      v.Tags = append(v.Tags, t)
    }
  }
  if err := tr.Err(); err != nil {
    return err
  }

  rendering := o.teaser || o.feed || o.render
  docs := map[int64]*content.Node{}
  var mediaIDs []int64
  for _, p := range rows {
    if p.mediaID != 0 {
      mediaIDs = append(mediaIDs, p.mediaID)
    }
    if rendering {
      doc, err := content.Parse(p.doc)
      if err != nil {
        return err
      }
      docs[p.ref.ID] = doc
      mediaIDs = append(mediaIDs, content.MediaIDs(doc)...)
    }
  }
  media, err := st.loadMedia(ctx, mediaIDs)
  if err != nil {
    return err
  }
  lookup := func(id int64) (content.MediaInfo, bool) {
    m, ok := media[id]
    return m.MediaInfo, ok
  }

  for _, p := range rows {
    v := p.view
    byName := func(a, b *theme.Term) int { return cmp.Compare(a.Name, b.Name) }
    slices.SortFunc(v.Categories, byName)
    slices.SortFunc(v.Tags, byName)
    if p.ref.CategoryID != 0 {
      v.Primary = snap.termView(p.ref.CategoryID)
    }
    if m, ok := media[p.mediaID]; ok {
      v.FeaturedMedia = snap.mediaView(m)
    }
    v.URL = snap.abs(snap.postPath(&p.ref))
    if p.ref.Type == "page" {
      chain := snap.pageChain(p.ref.ID)
      for _, a := range chain[:max(0, len(chain)-1)] {
        v.Ancestors = append(v.Ancestors, &theme.Post{ID: a.ID, WPID: wpID(a.ID, a.LegacyKey, "post"), Type: "page",
          Slug: a.Slug, Title: a.Title, URL: snap.abs(snap.pagePath(a.ID))})
      }
    }
    if !rendering {
      continue
    }
    rc := content.RenderContext{PostID: v.WPID, Media: lookup}
    if st.theme != nil {
      rc.Images = st.theme.Manifest().Images()
      if st.theme.HasPartials() {
        rc.Partials = st.theme
      }
    }
    doc := docs[p.ref.ID]
    if o.teaser {
      h, txt, more, err := content.RenderTeaser(doc, rc)
      if err != nil {
        return err
      }
      if more {
        v.Teaser, v.TeaserText, v.HasMore = template.HTML(h), txt, true
      }
    }
    if o.render || o.feed {
      if o.feed {
        rc.Feed, rc.BaseURL = true, snap.base
      }
      h, _, err := content.Render(doc, rc)
      if err != nil {
        return err
      }
      v.ContentHTML = template.HTML(h)
    }
  }
  return nil
}
