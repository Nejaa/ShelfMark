export const queryElement = selector => document.querySelector(selector);
export const FIELD_LABELS = {
    title: 'Title',
    subtitle: 'Subtitle',
    original_title: 'Original title',
    authors: 'Authors',
    translators: 'Translator(s)',
    series: 'Series',
    series_index: 'Series volume',
    publisher: 'Publisher',
    pubdate: 'Publication date',
    edition: 'Edition',
    collection: 'Collection',
    isbn: 'ISBN',
    identifiers: 'Other identifiers',
    catalog_url: 'Catalog URL',
    rating: 'Rating (out of 5)',
    languages: 'Language',
    tags: 'Categories',
    comments: 'Description',
    rights: 'Rights / copyright',
    cover_url: 'Cover image'
};
export const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
}[c]));
export const text = v => Array.isArray(v) ? v.join(', ') : String(v ?? '');

export async function api(path, data, signal) {
    const response = await fetch(path, {
        method: data === undefined ? 'GET' : 'POST',
        headers: data === undefined ? undefined : new Headers({'Content-Type': 'application/json'}),
        body: data === undefined ? undefined : JSON.stringify(data),
        signal
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || `Request failed (${response.status})`);
    return result;
}

export function openDialog(dialog) { if (!dialog.open) dialog.showModal(); }
export function closeDialog(dialog) { dialog.close(); }

// Browser exceptions otherwise never reach the application's log file. Reporting
// is best effort and independent of api(), so a reporting failure cannot recurse.
export function reportUIError(error, bookID = '') {
    console.error('Shelfmark UI error', error);
    void fetch('/api/ui-error', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
            message: String(error?.message || error || 'Unknown UI error').slice(0, 1000),
            stack: String(error?.stack || '').slice(0, 4000),
            book_id: bookID
        })
    }).catch(() => {});
}

window.addEventListener('error', event => reportUIError(event.error || event.message));
window.addEventListener('unhandledrejection', event => {
    if (event.reason?.name !== 'AbortError') reportUIError(event.reason);
});
