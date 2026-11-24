---
topic: control-plane
---

# D-new-dashboard-read-tools — `dashboard_list` and `dashboard_get` are advanced collection reads, and a missing dashboard is an empty one

**Decision.** `dashboard_list` and `dashboard_get` are read-only, advanced tools of the collection resource class. `dashboard_get` returns the dashboard resolved for the caller; a name with no file answers an empty dashboard with hash `""` instead of an error, and an invalid name is an error. A caller that does not read the whole KB gets the `text` of a `note` widget blank.

**Why.** An agent builds a dashboard from nothing with a patch guarded by the hash it read: an empty answer with hash `""` is that first read, with no special case. Entries, groups and counts already come from sources narrowed to what the caller reads (D226), so the tools need no whole-KB grant; a note's text is free text of the whole KB, not of a collection, so a narrowed caller does not read it. It costs a narrowed caller the note text it might have been meant to see.

**Alternatives rejected.** Whole-KB class: it would refuse every narrowed token a dashboard whose widgets resolve to what that token reads. A not-found error for a missing name: the agent would need a second path to start a dashboard. Core tools: the agent profile has no fixed-cost headroom for them.

**Consequences.** The write tools join `advancedToolNames` with their own change, because a registry test refuses a name no tool carries. A new free-text field of a widget decides in the resolver whether a narrowed caller reads it.
