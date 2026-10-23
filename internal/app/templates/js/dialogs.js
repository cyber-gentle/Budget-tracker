// Manage keyboard focus for the existing modal overlays.
(() => {
    const modals = [...document.querySelectorAll('[id$="-modal"]')].filter(el => el.classList.contains('fixed'));
    const previousFocus = new Map();
    const stack = [];
    const focusable = modal => [...modal.querySelectorAll('button, input, select, textarea, a[href], [tabindex]')]
        .filter(el => !el.disabled && el.tabIndex >= 0 && el.getClientRects().length);
    for (const modal of modals) {
        const panel = modal.firstElementChild;
        panel.setAttribute('role', 'dialog');
        panel.setAttribute('aria-modal', 'true');
        panel.setAttribute('aria-label', modal.id === 'add-tx-modal' ? 'Add transaction' : (panel.querySelector('h2,h3')?.textContent.trim() || 'Receipt viewer'));
        panel.tabIndex = -1;
    }
    const sync = () => {
        for (const modal of modals) {
            const open = !modal.classList.contains('hidden');
            const index = stack.indexOf(modal);
            if (open && index < 0) {
                previousFocus.set(modal, lastOutsideFocus);
                stack.push(modal);
                const target = modal.querySelector('input:not([type="hidden"]):not([disabled])') || focusable(modal)[0] || modal.firstElementChild;
                target.focus();
            } else if (!open && index >= 0) {
                const wasTop = index === stack.length - 1;
                stack.splice(index, 1);
                if (wasTop) previousFocus.get(modal)?.focus();
                previousFocus.delete(modal);
            }
        }
        document.body.style.overflow = stack.length ? 'hidden' : originalOverflow;
    };
    const originalOverflow = document.body.style.overflow;
    let lastOutsideFocus = document.activeElement;
    document.addEventListener('focusin', event => {
        const top = stack[stack.length - 1];
        if (!top) {
            if (!modals.some(modal => modal.contains(event.target))) lastOutsideFocus = event.target;
        } else if (!top.contains(event.target)) {
            (focusable(top)[0] || top.firstElementChild).focus();
        }
    });
    document.addEventListener('keydown', event => {
        const modal = stack[stack.length - 1];
        if (!modal) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            const close = modal.querySelector('button[id*="close"], button[id*="cancel"]');
            if (close) close.click(); else modal.classList.add('hidden');
        } else if (event.key === 'Tab') {
            const items = focusable(modal);
            const first = items[0] || modal.firstElementChild;
            const last = items[items.length - 1] || first;
            if (event.shiftKey && (document.activeElement === first || !items.includes(document.activeElement))) { event.preventDefault(); last.focus(); }
            else if (!event.shiftKey && (document.activeElement === last || !items.includes(document.activeElement))) { event.preventDefault(); first.focus(); }
        }
    });
    const observer = new MutationObserver(sync);
    modals.forEach(modal => observer.observe(modal, {attributes:true, attributeFilter:['class']}));
    sync();
})();
