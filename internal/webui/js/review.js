import {queryElement, api, esc, FIELD_LABELS, openDialog, closeDialog} from './common.js';

export function createReview(onChanged) {
    const stagedSaveButton = queryElement('#stagedSaveButton');
    const saveReviewPanel = queryElement('#saveReview');
    async function refreshStagedSaveButton() {
        try {
            const d = await api('/api/staged-count', {});
            stagedSaveButton.disabled = !d.count;
            stagedSaveButton.textContent = d.count ? `Review & save drafts (${d.count})` : 'No staged drafts'
        } catch {
            stagedSaveButton.disabled = true
        }
    }

    let stagedReviewBooks = [], loadingGeneration = 0, saving = false;
    saveReviewPanel.addEventListener('cancel', event => { if (saving) event.preventDefault(); });
    queryElement('#cancelSaveReview').onclick = () => closeDialog(saveReviewPanel);

    function updateSaveReviewSelection() {
        const master = queryElement('#reviewSelectAll'), bookChecks = [...document.querySelectorAll('.review-book-enabled')];
        const enabled = bookChecks.filter(c => c.checked).length;
        master.checked = bookChecks.length > 0 && enabled === bookChecks.length;
        master.indeterminate = enabled > 0 && enabled < bookChecks.length;
        for (const block of document.querySelectorAll('.review-book')) {
            const fields = [...block.querySelectorAll('.review-field')],
                selected = fields.filter(c => c.checked).length, all = block.querySelector('.review-book-master input');
            all.disabled = saving || !fields.length;
            all.checked = fields.length > 0 && selected === fields.length;
            all.indeterminate = selected > 0 && selected < fields.length
        }
        const any = stagedReviewBooks.some(book => {
            const block = [...document.querySelectorAll('.review-book')].find(node => node.dataset.path === book.path);
            return block?.querySelector('.review-book-enabled')?.checked && [...block.querySelectorAll('.review-field')].some(c => c.checked)
        });
        queryElement('#confirmSaveReview').disabled = saving || !any;
        queryElement('#discardSelectedDrafts').disabled = saving || !enabled;
        queryElement('#reviewSelectAll').disabled = saving || !bookChecks.length;
        document.querySelectorAll('.discard-draft').forEach(button => button.disabled = saving);
        bookChecks.forEach(input => input.disabled = saving);
        document.querySelectorAll('.review-field').forEach(input => input.disabled = saving)
    }

    function renderSaveReview() {
        const box = queryElement('#saveReviewItems');
        if (!stagedReviewBooks.length) {
            box.innerHTML = '<div class="review-empty">There are no staged metadata changes to save.</div>';
            queryElement('#reviewSelectAll').checked = false;
            queryElement('#reviewSelectAll').disabled = true;
            queryElement('#confirmSaveReview').disabled = true;
            queryElement('#discardSelectedDrafts').disabled = true;
            return
        }
        queryElement('#reviewSelectAll').disabled = false;
        box.innerHTML = stagedReviewBooks.map(book => {
            const title = book.metadata.title || book.file_metadata.title || book.name,
                fullTitle = book.metadata.subtitle ? `${title} — ${book.metadata.subtitle}` : title,
                entries = book.changes || [];
            return `<details class="review-book" data-path="${esc(book.path)}"><summary><input class="review-book-enabled" type="checkbox" checked aria-label="Select draft for ${esc(book.name)}"><img class="review-cover" src="${esc(book.cover || 'data:,')}" alt="Front page"><span class="review-headline"><span class="review-file">${esc(book.path)}</span><span class="review-title">${esc(fullTitle)}</span></span></summary><div class="review-meta">${book.unavailable ? `<p class="bad">Cannot save this book: ${esc(book.unavailable)}. Its draft can still be discarded.</p>` : ''}<label class="review-book-master"><input type="checkbox" checked> Select all metadata for this book</label><table><thead><tr><th>Save</th><th>Field</th><th>Current value</th><th>Value to save</th></tr></thead><tbody>${entries.map(({name: field, current, value}) => `<tr><td><input class="review-field" type="checkbox" data-field="${esc(field)}" checked aria-label="Save ${esc(FIELD_LABELS[field] || field)}"></td><td>${esc(FIELD_LABELS[field] || field)}</td><td>${field === 'cover_url' ? (book.cover ? '<img class="review-cover-preview" src="' + esc(book.cover) + '" alt="Current front page">' : '—') : esc(current || '—')}</td><td>${field === 'cover_url' ? '<img class="review-cover-preview" src="' + esc(value) + '" alt="Proposed cover">' : esc(value || '—')}</td></tr>`).join('')}</tbody></table><button type="button" class="quiet discard-draft" data-id="${esc(book.id)}">Discard draft</button></div></details>`
        }).join('');
        box.querySelectorAll('.discard-draft').forEach(button => {
            button.onclick = () => discardDrafts(stagedReviewBooks.filter(book => book.id === button.dataset.id));
        });
        box.querySelectorAll('.review-book-enabled').forEach(input => {
            input.onclick = e => e.stopPropagation();
            input.onchange = updateSaveReviewSelection
        });
        box.querySelectorAll('.review-book-master input').forEach(input => input.onchange = () => {
            input.closest('.review-book').querySelectorAll('.review-field').forEach(field => field.checked = input.checked);
            updateSaveReviewSelection()
        });
        box.querySelectorAll('.review-field').forEach(input => input.onchange = updateSaveReviewSelection);
        queryElement('#reviewSelectAll').onchange = () => {
            document.querySelectorAll('.review-book-enabled').forEach(input => input.checked = queryElement('#reviewSelectAll').checked);
            updateSaveReviewSelection()
        };
        box.querySelectorAll('.review-book summary').forEach(summary => summary.querySelector('.review-book-enabled').addEventListener('click', event => event.stopPropagation()));
        updateSaveReviewSelection()
    }

    async function openSaveReview() {
        if (saving) return;
        const generation = ++loadingGeneration;
        openDialog(saveReviewPanel);
        queryElement('#saveReviewItems').innerHTML = '<div class="review-empty">Loading staged changes…</div>';
        queryElement('#saveReviewStatus').textContent = '';
        queryElement('#saveReviewStatus').className = 'status';
        queryElement('#confirmSaveReview').disabled = true;
        queryElement('#discardSelectedDrafts').disabled = true;
        queryElement('#reviewSelectAll').disabled = true;
        stagedReviewBooks = [];
        try {
            const d = await api('/api/staged', {});
            if (generation !== loadingGeneration || !saveReviewPanel.open) return;
            if (d.warnings?.length) queryElement('#saveReviewStatus').textContent = d.warnings.join('; ');
            stagedReviewBooks = d.books || [];
            renderSaveReview()
        } catch (e) {
            queryElement('#saveReviewItems').innerHTML = `<div class="bad">${esc(e.message)}</div>`
        }
    }

    // Discarding removes the entire selected patch, independent of field checks.
    // The server resolves book IDs and validates versions; no paths are trusted.
    async function discardDrafts(books) {
        if (saving || !books.length) return;
        const description = books.length === 1 ? `the draft for ${books[0].name}` : `${books.length} selected drafts`;
        if (!window.confirm(`Discard ${description}? All staged changes for these books will be removed. The ebook files will not be modified.`)) return;

        saving = true;
        updateSaveReviewSelection();
        queryElement('#cancelSaveReview').disabled = true;
        const status = queryElement('#saveReviewStatus');
        status.className = 'status';
        status.textContent = 'Discarding drafts…';
        try {
            const result = await api('/api/drafts/discard', {
                books: books.map(book => ({id: book.id, draft_version: book.draft_version}))
            });
            // Reflect the committed outcome immediately, even if refresh fails.
            const discarded = new Set(result.books.map(book => book.id));
            stagedReviewBooks = stagedReviewBooks.filter(book => !discarded.has(book.id));
            renderSaveReview();
            await refreshStagedSaveButton();
            await onChanged(result.books);
            const refreshed = await api('/api/staged', {});
            stagedReviewBooks = refreshed.books || [];
            renderSaveReview();
            if (!stagedReviewBooks.length) closeDialog(saveReviewPanel);
            status.textContent = refreshed.warnings?.join('; ') || 'Selected drafts discarded.';
        } catch (error) {
            status.textContent = `Could not complete discard or refresh: ${error.message}`;
            status.className = 'status bad';
        } finally {
            saving = false;
            queryElement('#cancelSaveReview').disabled = false;
            updateSaveReviewSelection();
        }
    }

    queryElement('#discardSelectedDrafts').onclick = () => {
        const selected = stagedReviewBooks.filter(book => {
            const block = [...document.querySelectorAll('.review-book')].find(node => node.dataset.path === book.path);
            return block?.querySelector('.review-book-enabled')?.checked;
        });
        void discardDrafts(selected);
    };

    queryElement('#confirmSaveReview').onclick = async () => {
        if (saving) return;
        const selected = stagedReviewBooks.flatMap(book => {
            const block = [...document.querySelectorAll('.review-book')].find(node => node.dataset.path === book.path);
            if (book.unavailable || !block?.querySelector('.review-book-enabled')?.checked) return [];
            const fields = [...block.querySelectorAll('.review-field:checked')].map(c => c.dataset.field);
            return fields.length ? [{id: book.id, fields, fingerprint: book.fingerprint, draft_version: book.draft_version}] : []
        });
        if (!selected.length) return;
        const btn = queryElement('#confirmSaveReview');
        saving = true;
        btn.disabled = true;
        updateSaveReviewSelection();
        queryElement('#cancelSaveReview').disabled = true;
        queryElement('#saveReviewStatus').textContent = 'Saving selected changes…';
        try {
            const result = await api('/api/save', {books: selected});
            const failures = result.errors || [];
            const status = queryElement('#saveReviewStatus');
            // Render the server's outcome before refreshing other UI sections.
            // A failed write keeps its draft; it must not look like a saved book.
            status.className = failures.length ? 'status bad' : 'status';
            status.textContent = failures.map(f => `${f.path}: ${f.error}`).join('; ');
            const refreshed = await api('/api/staged', {});
            stagedReviewBooks = refreshed.books || [];
            await refreshStagedSaveButton();
            if (stagedReviewBooks.length || failures.length || refreshed.warnings?.length) {
                queryElement('#saveReviewStatus').textContent = [...failures.map(f => `${f.path}: ${f.error}`), ...(refreshed.warnings || [])].join('; ') || 'Selected changes saved. Remaining drafts are stored in the database.';
                renderSaveReview()
            } else {
                closeDialog(saveReviewPanel);
                queryElement('#saveReviewStatus').textContent = '';
            }
            await onChanged(result.books || []);
        } catch (e) {
            queryElement('#saveReviewStatus').textContent = `Could not complete save or refresh: ${e.message}`;
            queryElement('#saveReviewStatus').className = 'status bad';
        } finally {
            saving = false;
            queryElement('#cancelSaveReview').disabled = false;
            updateSaveReviewSelection();
        }
    };

    stagedSaveButton.onclick = openSaveReview;
    void refreshStagedSaveButton();
    return {openSaveReview, refreshStagedSaveButton};
}
