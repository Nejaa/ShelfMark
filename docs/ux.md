# UX improvements

The interface keeps scanning, matching, staging and writing as distinct actions.
Settings are a separate page within the SPA, grouped into Library, Catalogs and
Matching. New groups can be added without expanding a single API-key dialog.

## Available now

- Filter the library by filename, title or author.
- Identify staged books in the list and review persisted drafts after restarting.
- Navigate books and matches with the keyboard; dialogs manage focus and Escape.
- Edit descriptions over several lines and clear metadata fields intentionally.
- Stop background matching, and see catalog failures alongside useful results.
- Keep API keys masked with a password field; saving other settings preserves the key.
- Use small screens with stacked panels and scrollable metadata tables.
- See folder paths as paths on the server, including when browsing remotely.

## Proposed next steps

1. **Backup and undo.** Offer configurable backups before replacing files, with a
   retention policy and a restore action. This needs a file recovery workflow,
   not just a toggle in Settings.
2. **Scan progress.** Show discovered/read/skipped counts and cancellation for
   large collections. Separate job state from the current selection so users can
   keep reviewing while a scan runs.
3. **Draft management.** Add a drafts-only library filter, discard actions, and
   recovery for books that were moved or deleted. Discard should show what will
   be lost and leave book files unchanged.
4. **Metadata profiles.** Allow users to choose visible fields and default field
   selection for catalog matches. Keep manual editing available for every
   supported field and show format-specific limitations beside the control.
5. **Provider details.** Expose retry actions, explain work-level versus
   edition-level results, and show which returned fields actually contributed
   to the match score.
6. **Display preferences.** Add light/system themes and adjustable list density
   after defining corresponding design tokens and checking contrast across
   states. Keep these browser preferences separate from server-wide settings.

For larger libraries, use a paginated or virtualized list before adding more
client-side filters. Background work currently belongs to the open browser;
continuing jobs after it closes would require server-owned job state.
