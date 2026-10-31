// Spendly export window: opens the shared "export-modal" template and
// downloads /api/export with the chosen format, period, wallet and type.
// Reports' period names map onto the window's options.
const EXPORT_TIMEFRAME_ALIASES = { ytd: 'this_year', all: 'all_time', '1y': '12m', quarter: '3m' };

async function fillExportWallets(select, selected) {
    select.innerHTML = '<option value="all">All Wallets</option>';
    try {
        const res = await fetch('/api/accounts');
        if (res.ok) {
            const data = await res.json();
            for (const a of (data.accounts || [])) {
                const opt = document.createElement('option');
                opt.value = a.id;
                opt.textContent = `${a.icon || '🏦'} ${a.name}`;
                select.appendChild(opt);
            }
        }
    } catch (e) { /* the export still works for all wallets */ }
    if (selected && [...select.options].some(o => o.value === String(selected))) select.value = String(selected);
}

function syncExportCustomDates() {
    const tf = document.getElementById('export-timeframe');
    const box = document.getElementById('export-custom-dates');
    if (!tf || !box) return;
    const custom = tf.value === 'custom';
    box.classList.toggle('hidden', !custom);
    // Both dates are needed for a custom range; the browser explains a missing one.
    box.querySelectorAll('input[type="date"]').forEach(i => { i.required = custom; });
}

function openExportModal(defaults = {}) {
    const modal = document.getElementById('export-modal');
    if (!modal) return;
    const tf = document.getElementById('export-timeframe');
    if (tf && defaults.timeframe) {
        const v = EXPORT_TIMEFRAME_ALIASES[defaults.timeframe] || defaults.timeframe;
        if ([...tf.options].some(o => o.value === v)) tf.value = v;
    }
    if (defaults.start) document.getElementById('export-start').value = defaults.start;
    if (defaults.end) document.getElementById('export-end').value = defaults.end;
    const type = document.getElementById('export-type');
    if (type && defaults.type) type.value = defaults.type;
    syncExportCustomDates();
    const acc = document.getElementById('export-account');
    if (acc) fillExportWallets(acc, defaults.account_id);
    modal.classList.remove('hidden');
}

function closeExportModal() {
    document.getElementById('export-modal')?.classList.add('hidden');
}

function initExportModal() {
    const modal = document.getElementById('export-modal');
    const form = document.getElementById('export-form');
    if (!modal || !form) return;
    // Header button and avatar-menu item. A page can pre-set the window
    // through window.spendlyExportDefaults (Reports passes the period being viewed).
    for (const id of ['btn-export-header', 'btn-dropdown-export']) {
        document.getElementById(id)?.addEventListener('click', (e) => {
            e.preventDefault();
            document.getElementById('user-dropdown-menu')?.classList.add('hidden');
            openExportModal(typeof window.spendlyExportDefaults === 'function' ? window.spendlyExportDefaults() : {});
        });
    }
    document.getElementById('export-modal-close')?.addEventListener('click', closeExportModal);
    document.getElementById('export-cancel')?.addEventListener('click', closeExportModal);
    modal.addEventListener('click', (e) => { if (e.target === modal) closeExportModal(); });
    document.getElementById('export-timeframe')?.addEventListener('change', syncExportCustomDates);
    form.addEventListener('submit', (e) => {
        e.preventDefault();
        const params = new URLSearchParams();
        params.set('format', form.querySelector('input[name="export-format"]:checked')?.value || 'csv');
        const timeframe = document.getElementById('export-timeframe').value;
        params.set('timeframe', timeframe);
        if (timeframe === 'custom') {
            params.set('start', document.getElementById('export-start').value);
            params.set('end', document.getElementById('export-end').value);
        }
        const account = document.getElementById('export-account').value;
        if (account && account !== 'all') params.set('account_id', account);
        const type = document.getElementById('export-type').value;
        if (type && type !== 'all') params.set('type', type);
        closeExportModal();
        window.location.href = `/api/export?${params.toString()}`;
    });
}
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', initExportModal);
else initExportModal();
