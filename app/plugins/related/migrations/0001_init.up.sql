CREATE TABLE plg_related_items (
  post_id    bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  related_id bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  score      real   NOT NULL,
  PRIMARY KEY (post_id, related_id)
);
CREATE INDEX plg_related_items_score_idx ON plg_related_items (post_id, score DESC);
