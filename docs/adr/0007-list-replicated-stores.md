# ADR 0007: Judge a replicated store by listing it

- Status: accepted
- Date: 2026-10-08

## Context
`disavery attachments copy` fills a site store from another store and skips
objects the target already holds; it asked the target with a HEAD request per
object. In the first S4 run the attacker had wiped site a's store, yet the
copy reported "copied 0, already present 495" and the drill's consistency
check then found 42 documents without an attachment.

A MinIO bucket with replication configured answers a GET or HEAD for an object
it does not hold by proxying the request to its replication target. Site a's
store replicates to the vault, so it claimed to hold every attachment the
vault had, while listing it showed only the few written since the wipe.

## Decision
- What a store holds is decided by listing it, never by per-object HEAD or GET:
  the copy lists the target first and copies every current source object that
  is missing or differs in size or ETag.
- The consistency and store-sync checks already list, and stay that way.

## Consequences
- A wiped store is refilled completely; the S4 re-run passed with every
  attachment present in site a's own store.
- The application keeps reading through the proxy, which can hide a missing
  attachment from users; only the drills' listing-based checks see it.
- One extra listing per copy: negligible at the lab's size.
