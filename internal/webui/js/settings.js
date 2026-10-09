import {queryElement, api} from './common.js';

// Settings fields use their API names, so new controls need no separate mapping.
export function initSettings(onChange) {
    const form = queryElement('#settingsForm');
    let loaded = false;
    let dirty = false;
    let ocrAvailable = null;

    function updateOCRWarning() {
        // Availability describes the server, including when the UI is remote.
        // Show the warning immediately when toggling OCR, before saving.
        queryElement('#ocrWarning').hidden = !form.elements.namedItem('ocr').checked || ocrAvailable !== false;
    }

    function populate(settings) {
        for (const input of form.querySelectorAll('[name]')) {
            if (input.type === 'checkbox') input.checked = settings[input.name];
            else input.value = settings[input.name];
        }
        queryElement('#googleBooksKey').value = '';
        queryElement('#removeGoogleKey').checked = false;
        queryElement('#googleKeyStatus').textContent = settings.google_books_api_key_configured ? 'A key is saved on this server.' : 'No key saved; Google Books uses the public quota.';
        ocrAvailable = settings.ocr_status.available;
        queryElement('#ocrWarning').textContent = settings.ocr_status.warning || 'OCR is unavailable on this server. Check shelfmark.log for details.';
        updateOCRWarning();
        loaded = true;
        dirty = false;
        queryElement('#saveSettings').disabled = true;
        onChange(settings);
    }
    function showPage(settings) {
        queryElement('.scan').classList.toggle('hidden', settings);
        queryElement('.layout').classList.toggle('hidden', settings);
        queryElement('#settingsPage').classList.toggle('hidden', !settings);
        queryElement('#settingsButton').setAttribute('aria-current', settings ? 'page' : 'false');
        queryElement('#libraryButton').setAttribute('aria-current', settings ? 'false' : 'page');
    }
    queryElement('#settingsButton').onclick = async () => {
        showPage(true);
        if (dirty) return;
        try { populate(await api('/api/settings')); }
        catch (error) { queryElement('#settingsStatus').textContent = error.message; }
    };
    queryElement('#libraryButton').onclick = () => showPage(false);
    form.addEventListener('input', () => {
        dirty = true;
        queryElement('#saveSettings').disabled = !loaded;
        queryElement('#settingsStatus').textContent = 'Unsaved changes';
        updateOCRWarning();
    });
    window.addEventListener('beforeunload', event => {
        if (dirty) event.preventDefault();
    });
    form.onsubmit = async event => {
        event.preventDefault();
        if (!loaded) return;
        const patch = {};
        for (const input of form.querySelectorAll('[name]')) {
            patch[input.name] = input.type === 'checkbox' ? input.checked : input.type === 'number' ? Number(input.value) : input.value.trim();
        }
        if (queryElement('#removeGoogleKey').checked) patch.google_books_api_key = '';
        else if (queryElement('#googleBooksKey').value.trim()) patch.google_books_api_key = queryElement('#googleBooksKey').value.trim();
        queryElement('#saveSettings').disabled = true;
        try {
            populate(await api('/api/settings', patch));
            queryElement('#settingsStatus').textContent = 'Settings saved.';
        } catch (error) {
            queryElement('#settingsStatus').textContent = error.message;
            queryElement('#saveSettings').disabled = false;
        }
    };
    api('/api/settings').then(settings => {
        populate(settings);
        queryElement('#settingsStatus').textContent = '';
    }).catch(error => { queryElement('#settingsStatus').textContent = `Could not load settings: ${error.message}`; });
}
