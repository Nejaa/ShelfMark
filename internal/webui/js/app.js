import {queryElement, api, esc, text, reportUIError, openDialog, closeDialog} from './common.js';
import {createReview} from './review.js';
import {createEditor} from './editor.js';
import {initSettings} from './settings.js';

/** @type {import('./types.js').Candidate[]} */
let candidates = [];
/** @type {import('./types.js').Book[]} */
let books = [];
/** @type {import('./types.js').Book|null} */
let active = null;
let folders = [], folderCount = 0, chosen = -1, libraryRoot = '', activeFolder = '';
let selectionGeneration = 0, listGeneration = 0, searchController;
let pickerPath = '', backgroundID = '', backgroundGeneration = 0, backgroundTimer, filterTimer;

function updateBulkMatchesButton() {
    const btn = queryElement('#bulkMatches');
    btn.disabled = !folderCount || Boolean(backgroundID);
    btn.textContent = `Background matches (${folderCount} books)`;
}

function renderBackground(progress) {
    const running = progress.status === 'running';
    backgroundID = running ? progress.id : '';
    queryElement('#cancelMatches').hidden = !running;
    const btn = queryElement('#bulkMatches');
    btn.disabled = running || !folderCount;
    if (running) btn.textContent = `Background matches ${progress.completed}/${progress.total}…`;
    else if (progress.status === 'completed') btn.textContent = `Background matches complete (${progress.total}${progress.failures ? `, ${progress.failures} with errors` : ''})`;
    else if (progress.status === 'failed') btn.textContent = `Background matches stopped (${progress.completed}/${progress.total}, ${progress.failures} with errors)`;
    else updateBulkMatchesButton();
}

// Polling only renders progress; the server owns the queue, delays and priority.
async function refreshBackground(generation) {
    try {
        const progress = await api('/api/background');
        if (generation !== backgroundGeneration) return;
        renderBackground(progress);
        if (progress.status === 'running') backgroundTimer = setTimeout(() => refreshBackground(generation), 500);
    } catch (error) {
        if (generation !== backgroundGeneration) return;
        queryElement('#scanStatus').textContent = error.message;
        backgroundTimer = setTimeout(() => refreshBackground(generation), 2000);
    }
}

async function cancelBackground() {
    clearTimeout(backgroundTimer);
    const generation = ++backgroundGeneration;
    if (backgroundID) {
        const progress = await api('/api/background/cancel', {id: backgroundID});
        if (generation === backgroundGeneration) {
            renderBackground(progress);
            if (progress.status === 'running') await refreshBackground(generation);
        }
    }
}

queryElement('#bulkMatches').onclick = async () => {
    queryElement('#bulkMatches').disabled = true;
    try {
        clearTimeout(backgroundTimer);
        const generation = ++backgroundGeneration;
        const progress = await api('/api/background/start', {folder: activeFolder});
        if (generation !== backgroundGeneration) return;
        renderBackground(progress);
        await refreshBackground(generation);
    } catch (error) {
        queryElement('#scanStatus').textContent = error.message;
        updateBulkMatchesButton();
    }
};
queryElement('#cancelMatches').onclick = () => cancelBackground().catch(error => {
    queryElement('#scanStatus').textContent = error.message;
});

async function refreshLibrary() {
    if (!libraryRoot) return;
    const generation = ++listGeneration;
    const result = await api('/api/library', {folder: activeFolder, filter: queryElement('#libraryFilter').value});
    if (generation !== listGeneration) return;
    bindLibrary(result);
}

function bindLibrary(result) {
    books = result.books;
    folders = result.folders;
    folderCount = result.folder_count;
    libraryRoot = result.root;
    activeFolder = result.folder;
    queryElement('#count').textContent = `(${result.total})`;
    renderTree();
    renderBooks();
    updateBulkMatchesButton();
}

queryElement('#scan').onclick = async () => {
    let b = queryElement('#scan');
    b.disabled = true;
    queryElement('#scanStatus').textContent = 'Scanning files and reading metadata…';
    try {
        let d = await api('/api/scan', {root: queryElement('#root').value});
        selectionGeneration++;
        listGeneration++;
        searchController?.abort();
        resetEditor();
        queryElement('#detail').className = 'empty';
        queryElement('#detail').textContent = 'Select a book to review its metadata.';
        queryElement('#scanStatus').className = 'note';
        queryElement('#scanStatus').textContent = d.warnings?.length ? `${d.total} books. ${d.warnings.join('; ')}` : `${d.total} books found.`;
        active = null;
        bindLibrary(d);
        await refreshLibrary();
        clearTimeout(backgroundTimer);
        await refreshBackground(++backgroundGeneration);
    } catch (e) {
        queryElement('#scanStatus').textContent = e.message;
        queryElement('#scanStatus').className = 'note bad'
    } finally {
        b.disabled = false
    }
};

function renderTree() {
    const box = queryElement('#folderTree');
    if (!libraryRoot) {
        box.innerHTML = '';
        return
    }
    box.innerHTML = folders.map(folder => `<button class="treeitem ${folder.path === activeFolder ? 'current' : ''}" style="--depth:${folder.depth}" data-path="${esc(folder.path)}">${folder.depth === 0 ? '▾ ' : '📁 '}${esc(folder.name)}</button>`).join('');
    box.querySelectorAll('.treeitem').forEach(el => el.onclick = async () => {
        try {
            await cancelBackground();
            activeFolder = el.dataset.path;
            await refreshLibrary();
        } catch (error) { queryElement('#scanStatus').textContent = error.message; }
    });
}

function renderBooks() {
    const box = queryElement('#books'), query = queryElement('#libraryFilter').value;
    if (!books.length) {
        box.innerHTML = query ? '<div class="empty">No books match this filter.</div>' : '<div class="empty">No supported ebooks in this folder.</div>';
        return;
    }
    box.innerHTML = books.map(b => {
        const title = b.metadata.title || b.name, subtitle = b.metadata.subtitle;
        return `<button type="button" class="book ${b.staged ? 'staged' : ''} ${active?.id === b.id ? 'selected' : ''}" data-id="${b.id}"><strong>${esc(title)}${subtitle ? ': ' + esc(subtitle) : ''}</strong><small>${esc(text(b.metadata.authors) || b.name)}</small></button>`;
    }).join('');
    box.querySelectorAll('.book').forEach(el => el.onclick = () => select(books.find(b => b.id === el.dataset.id)));
}

async function browse(path) {
    const d = await api('/api/browse', {path});
    pickerPath = d.path;
    queryElement('#pickPath').textContent = pickerPath;
    queryElement('#pickParent').disabled = !d.parent;
    queryElement('#pickParent').onclick = () => browse(d.parent).catch(showBrowseError);
    queryElement('#pickDirs').innerHTML = d.dirs.map(x => `<button class="dir" data-path="${esc(x.path)}">📁 ${esc(x.name)}</button>`).join('') || '<div class="muted">No subfolders</div>';
    queryElement('#pickDirs').querySelectorAll('.dir').forEach(el => el.onclick = () => browse(el.dataset.path).catch(showBrowseError))
}

queryElement('#pick').onclick = async () => {
    openDialog(queryElement('#picker'));
    try { await browse(queryElement('#root').value); }
    catch (error) { showBrowseError(error); }
};
function showBrowseError(error) { queryElement('#pickDirs').innerHTML = `<p class="bad">${esc(error.message)}</p>`; }
queryElement('#useFolder').onclick = async () => {
    try {
        await api('/api/settings', {last_folder: pickerPath});
        queryElement('#root').value = pickerPath;
        closeDialog(queryElement('#picker'));
        queryElement('#scan').click();
    } catch (e) {
        queryElement('#pickDirs').innerHTML = `<p class="bad">Could not save the last folder: ${esc(e.message)}</p>`
    }
};
queryElement('#cancelPicker').onclick = () => closeDialog(queryElement('#picker'));

async function select(b) {
    const generation = ++selectionGeneration;
    searchController?.abort();
    searchController = new AbortController();
    const signal = searchController.signal;
    active = b;
    candidates = [];
    chosen = -1;
    renderBooks();
    queryElement('#detail').className = 'detail';
    queryElement('#detail').innerHTML = `<div class="bookhead"><div id="coverbox" class="cover"></div><div class="bookinfo"><h3>${esc(b.metadata.title || b.name)}</h3><p>${esc(b.path)}</p><p>${esc(text(b.metadata.authors) || 'No author metadata')}</p></div></div><div class="searchrow"><input aria-label="Catalog search terms" id="query" value="${esc(b.search_query)}" placeholder="Title, author, or ISBN"><button id="search">Find matches</button><label class="cache-option" title="Search catalogs again and refresh the cache"><input id="ignoreCache" type="checkbox"> Ignore cache</label></div><p class="note">Search terms are sent to enabled catalogs. Local ISBN extraction and optional OCR follow your settings.</p><div id="matches"></div><div id="compare"></div>`;
    queryElement('#search').onclick = search;
    queryElement('#query').addEventListener('keydown', event => { if (event.key === 'Enter') search(); });
    void renderCompare();
    void loadCachedMatches(b, generation, signal);
    try {
        const d = await api('/api/cover', {id: b.id});
        if (generation !== selectionGeneration) return;
        if (d.cover) queryElement('#coverbox').innerHTML = `<img class="cover" src="${esc(d.cover)}" alt="Current cover">`
    } catch {
    }
}

async function loadCachedMatches(book, generation, signal) {
    try {
        const result = await api('/api/search/cached', {id: book.id}, signal);
        // A later selection or explicit search owns the UI once it aborts this
        // request. Also leave edited search terms alone while the cache loads.
        if (signal.aborted || generation !== selectionGeneration ||
            queryElement('#query')?.value !== book.search_query || queryElement('#ignoreCache')?.checked) return;
        candidates = result.candidates || [];
        if (candidates.length) {
            renderMatches();
            if (result.warnings?.length) queryElement('#matches').insertAdjacentHTML('afterbegin', `<p class="warning">Previous search: ${esc(result.warnings.join('; '))}</p>`);
        }
    } catch (error) {
        if (signal.aborted || generation !== selectionGeneration) return;
        queryElement('#matches').innerHTML = `<p class="warning">Could not load cached matches: ${esc(error.message)}. Use Find matches to search again.</p>`;
    }
}

async function search() {
    if (queryElement('#search').disabled) return;
    const b = active, query = queryElement('#query').value, btn = queryElement('#search'), generation = selectionGeneration;
    searchController?.abort();
    searchController = new AbortController();
    btn.disabled = true;
    btn.textContent = 'Searching…';
    queryElement('#matches').innerHTML = '<p class="muted">Manual lookup has priority; background matching will resume afterward.</p>';
    try {
        const d = await api('/api/search', {id: b.id, ...(query === b.search_query ? {} : {query}), priority: 'manual', ignore_cache: queryElement('#ignoreCache').checked}, searchController.signal);
        if (generation !== selectionGeneration) return;
        candidates = d.candidates || [];
        renderMatches();
        if (d.warnings?.length) queryElement('#matches').insertAdjacentHTML('afterbegin', `<p class="warning">${esc(d.warnings.join('; '))}</p>`)
    } catch (e) {
        if (e instanceof TypeError) reportUIError(e, b.id);
        if (generation === selectionGeneration && e.name !== 'AbortError') queryElement('#matches').innerHTML = `<p class="bad">${esc(e.message)}</p>`
    } finally {
        btn.disabled = false;
        btn.textContent = 'Find matches'
    }
}

function renderMatches() {
    let box = queryElement('#matches');
    if (!candidates.length) {
        box.innerHTML = '<p class="warning">No matches found. Try a shorter title or an ISBN.</p>';
        return
    }
    box.innerHTML = `<details class="match-results" ${chosen < 0 ? 'open' : ''}><summary>Possible editions (${candidates.length}) — choose the closest match</summary><div class="matches">${candidates.map((c, i) => `<button type="button" class="match ${chosen === i ? 'chosen' : ''}" data-i="${i}">${c.cover ? `<img src="${esc(c.cover)}" loading="lazy" alt="Edition cover">` : ''}<strong>${esc(c.display_title || c.title)}</strong><small>${esc(text(c.authors))}</small><small>${esc([c.publisher, c.pubdate, c.isbn, c.source].filter(Boolean).join(' · '))}</small></button>`).join('')}</div></details>`;
    box.querySelectorAll('.match').forEach(el => el.onclick = () => choose(+el.dataset.i))
}

function choose(i) {
    chosen = i;
    renderMatches();
    void renderCompare();
}

async function apply() {
    const btn = queryElement('#apply');
    const book = active, generation = selectionGeneration;
    btn.disabled = true;
    queryElement('#applyStatus').textContent = 'Saving draft in database…';
    try {
        const selection = selectedValues();
        const d = await api('/api/apply', {id: book.id, ...selection});
        const defaultQuery = queryElement('#query')?.value === book.search_query;
        Object.assign(book, d);
        if (generation === selectionGeneration && defaultQuery) queryElement('#query').value = book.search_query;
        await refreshLibrary();
        await refreshStagedSaveButton();
        if (generation !== selectionGeneration) return;
        chosen = -1;
        renderMatches();
        await renderCompare();
        if (generation !== selectionGeneration) return;
        const status = queryElement('#applyStatus');
        if (status) status.textContent = 'Draft saved in database. Review and save to file when ready.'
    } catch (e) {
        if (generation !== selectionGeneration) return;
        const status = queryElement('#applyStatus');
        if (status) { status.textContent = e.message; status.className = 'status bad'; }
        btn.disabled = false
    }
}

queryElement('#libraryFilter').oninput = () => {
    clearTimeout(filterTimer);
    filterTimer = setTimeout(() => refreshLibrary().catch(error => {
        queryElement('#scanStatus').textContent = error.message;
    }), 150);
};
queryElement('#root').addEventListener('keydown', event => { if (event.key === 'Enter') queryElement('#scan').click(); });
const {renderCompare, selectedValues, resetEditor} = createEditor({
    getState: () => ({active, candidates, chosen}),
    onApply: apply,
    onSave: () => openSaveReview()
});
const {openSaveReview, refreshStagedSaveButton} = createReview(async changedBooks => {
    const changedActive = changedBooks.find(book => book.path === active?.path);
    const generation = selectionGeneration;
    const defaultQuery = changedActive && queryElement('#query')?.value === active.search_query;
    if (changedActive) Object.assign(active, changedActive);
    await refreshLibrary();
    if (changedActive && generation === selectionGeneration) {
        const refreshedActive = books.find(book => book.id === active?.id);
        if (refreshedActive) Object.assign(active, refreshedActive);
        if (defaultQuery && queryElement('#query')) queryElement('#query').value = active.search_query;
        chosen = -1;
        renderMatches();
        await renderCompare();
    }
});
initSettings(settings => {
    if (!queryElement('#root').value) queryElement('#root').value = settings.last_folder;
});
void refreshBackground(backgroundGeneration);

// A native helper announces readiness only after the SPA has initialized.
/** @type {Window & {shelfmarkWindowReady?: () => Promise<void>}} */
const desktopWindow = window;
if (typeof desktopWindow.shelfmarkWindowReady === 'function') {
    desktopWindow.shelfmarkWindowReady().catch(error => console.warn('Desktop readiness notification failed:', error));
}
