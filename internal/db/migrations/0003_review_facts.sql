-- Review-thread replies (#105). A reply inside a review thread reaches the
-- API as an implicit COMMENTED review holding that one comment, and the
-- PR walk cannot tell it from a real review. The two facts that can are
-- fetched by review id after the walk and kept here, beside reviews
-- rather than on it: SavePullRequests replaces a PR's reviews on every
-- re-sync, and a fact on the row would be wiped and re-fetched each time.
-- No foreign key for the same reason; a facts row whose review is gone is
-- harmless, and a review without a facts row is simply not classified yet.
--
-- has_body and first_reply are both NULL when GitHub no longer has the
-- review: that row is classified ("unknown") so it is never re-fetched,
-- and the view below leaves it counted as the review it was.

CREATE TABLE review_facts (
  review_id   TEXT PRIMARY KEY,
  has_body    INTEGER, -- 1 when the review carries a summary body
  first_reply INTEGER  -- 1 when its first comment replies to an existing thread
);

-- The one definition of a thread reply: a COMMENTED review with no body
-- and exactly one comment, which replies to a thread somebody else
-- opened. Metric SQL says `r.id NOT IN (SELECT id FROM thread_replies)`
-- and spells out none of this itself. An unclassified review is not in
-- the view, so it counts as it always did until the facts arrive.
CREATE VIEW thread_replies AS
  SELECT r.id
  FROM reviews r
  JOIN review_facts f ON f.review_id = r.id
  WHERE r.state = 'COMMENTED'
    AND r.comment_count = 1
    AND f.has_body = 0
    AND f.first_reply = 1;
