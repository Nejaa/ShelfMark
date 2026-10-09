import {queryElement, api, esc, FIELD_LABELS} from './common.js';

// The backend supplies field values, differences, defaults and capabilities.
// This module owns only form text, selection state and editor presentation.
export function createEditor({getState, onApply, onSave}) {
    let generation = 0, controller, pending = false, valid = false, edits = {}, autoSelection = new Set();

    function selectedValues() {
        const fields = [...document.querySelectorAll('.apply-field:checked')].map(input => input.dataset.field);
        const {candidates, chosen} = getState();
        const selectedEdits = {...edits};
        if (queryElement('#applyCover')?.checked) {
            fields.push('cover_url');
            selectedEdits.cover_url = candidates[chosen].cover;
        }
        return {fields, candidate: candidates[chosen], values: selectedEdits};
    }

    function syncSelection() {
        const fields = [...document.querySelectorAll('.apply-field')].filter(input => !input.disabled);
        const selected = fields.filter(input => input.checked).length;
        const master = queryElement('#applyAll');
        if (master) {
            master.disabled = !fields.length || pending;
            master.checked = fields.length > 0 && selected === fields.length;
            master.indeterminate = selected > 0 && selected < fields.length;
        }
        const button = queryElement('#apply');
        if (button) button.disabled = pending || !valid || (!selected && !queryElement('#applyCover')?.checked);
    }

    async function evaluateEdit() {
        controller?.abort();
        controller = new AbortController();
        const request = ++generation;
        pending = true;
        valid = false;
        syncSelection();
        const {active, candidates, chosen} = getState();
        try {
            const result = await api('/api/proposal', {id: active.id, candidate: candidates[chosen], values: edits}, controller.signal);
            if (request !== generation) return;
            for (const row of result.fields) {
                const input = document.querySelector(`.apply-field[data-field="${row.name}"]`);
                input.disabled = !row.changed || !row.supported;
                input.closest('tr').querySelector('.evidence').textContent = row.evidence || (row.changed ? 'Edited proposal' : 'Unchanged');
                if (input.disabled) input.checked = false;
                else if (autoSelection.has(row.name)) input.checked = row.suggested;
            }
            autoSelection.clear();
            valid = true;
            queryElement('#applyStatus').textContent = '';
        } catch (error) {
            if (request !== generation || error.name === 'AbortError') return;
            queryElement('#applyStatus').textContent = error.message;
            // Leave Apply disabled until the server accepts the edited values.
        } finally {
            if (request === generation) {
                pending = false;
                syncSelection();
            }
        }
    }

    async function renderCompare() {
        controller?.abort();
        controller = new AbortController();
        const request = ++generation;
        const {active, candidates, chosen} = getState();
        if (!active) return;
        const candidate = candidates[chosen] || {};
        pending = true;
        queryElement('#compare').innerHTML = '<p class="muted">Loading metadata editor…</p>';
        try {
            const result = await api('/api/proposal', {id: active.id, candidate: candidates[chosen]}, controller.signal);
            if (request !== generation) return;
            edits = {};
            autoSelection.clear();
            const sourceUrl = typeof candidate.url === 'string' && candidate.url.startsWith('https://') ? ` <a href="${esc(candidate.url)}" target="_blank" rel="noopener">Open source record ↗</a>` : '';
            const rows = result.fields.map(row => {
                const label = FIELD_LABELS[row.name] || row.name;
                return `<tr><td><input class="apply-field" type="checkbox" data-field="${row.name}" ${row.suggested ? 'checked' : ''} ${row.changed && row.supported ? '' : 'disabled'} aria-label="Apply ${esc(label)}"></td><td>${esc(label)}</td><td class="${row.changed ? 'old' : 'unchanged'}">${esc(row.current || '—')}</td><td>${row.name === 'comments' ? `<textarea class="proposal-input" data-field="${row.name}" aria-label="Proposed description">${esc(row.value)}</textarea>` : `<input class="proposal-input ${row.changed ? 'new' : 'unchanged'}" data-field="${row.name}" value="${esc(row.value)}" aria-label="Proposed ${esc(label)}">`}</td><td class="note evidence">${esc(row.evidence || (row.changed ? candidate.field_sources?.[row.name] || candidate.source || 'Manual edit' : 'Unchanged'))}${row.review_required && row.changed ? '<br><strong>Review before selecting</strong>' : ''}</td></tr>`;
            }).join('');
            const cover = candidate.cover ? `<div class="coverreview"><h3>Cover image</h3><img src="${esc(candidate.cover)}" alt="Proposed cover" class="cover proposedcover"><label><input id="applyCover" type="checkbox" ${result.cover_editable ? '' : 'disabled'}> Use this cover${result.cover_editable ? '' : ' (unavailable for this format)'}</label></div>` : '<p class="note">No cover image was returned by this source.</p>';
            const confidence = Math.round((candidate.confidence || 0) * 100);
            queryElement('#compare').innerHTML = `<div class="section"><h3>${candidate.source ? `Selected source: ${esc(candidate.source)}${sourceUrl}` : 'Edit metadata'}</h3>${candidate.source ? `<p><strong>Match score: ${confidence}%</strong> <span class="note">Heuristic score. Verify the edition details.</span></p>` : ''}${result.notes ? `<p class="note">${esc(result.notes)}</p>` : ''}<p class="note">Fields with conflicting or inferred values start unchecked. Review them before selecting.<br>Apply selected fields to the database draft, then save to write the draft to the file.</p>${cover}<h3>Review metadata</h3><table class="compare"><thead><tr><th><label>Apply<input id="applyAll" type="checkbox" aria-label="Select all changed fields"></label></th><th>Field</th><th>Working value</th><th>Proposed value</th><th>Source</th></tr></thead><tbody>${rows}</tbody></table><div class="actions"><button id="apply" disabled>Apply selected to draft</button><button id="saveFile" class="quiet" ${active.staged ? '' : 'disabled'}>Review &amp; save…</button><span class="note">${active.staged ? 'Draft changes are stored in the local database.' : 'Nothing staged yet.'}</span><span id="applyStatus" class="status"></span></div></div>`;
            document.querySelectorAll('.proposal-input').forEach(input => input.oninput = () => {
                const field = input.dataset.field;
                edits[field] = input.value;
                autoSelection.add(field);
                input.closest('tr').querySelector('.evidence').textContent = 'Manual edit';
                void evaluateEdit();
            });
            document.querySelectorAll('.apply-field').forEach(input => input.onchange = () => { autoSelection.delete(input.dataset.field); syncSelection(); });
            queryElement('#applyCover')?.addEventListener('change', syncSelection);
            queryElement('#applyAll').onchange = event => {
                document.querySelectorAll('.apply-field:not(:disabled)').forEach(input => input.checked = event.target.checked);
                syncSelection();
            };
            queryElement('#apply').onclick = onApply;
            queryElement('#saveFile').onclick = onSave;
            pending = false;
            valid = true;
            syncSelection();
        } catch (error) {
            if (request === generation && error.name !== 'AbortError') queryElement('#compare').innerHTML = `<p class="bad">${esc(error.message)}</p>`;
        }
    }

    function resetEditor() {
        generation++;
        controller?.abort();
        edits = {};
        valid = false;
    }

    return {renderCompare, selectedValues, resetEditor};
}
