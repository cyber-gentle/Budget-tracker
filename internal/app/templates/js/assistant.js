// Spendly Assistant: floating chat over the signed-in user's own finances.
// Proposed changes are shown as a card and only saved after the user taps Confirm.
(function () {
    if (window.__spendlyAssistant) return;
    window.__spendlyAssistant = true;

    const STORE_KEY = 'spendly_assistant_history';
    const MAX_HISTORY = 10;
    const SUGGESTIONS = [
        'How much did I spend this month?',
        'Am I over any budget?',
        'What are my biggest expenses lately?',
        'Add 2000 lunch today',
    ];

    const css = `
    .sa-fab{position:fixed;right:16px;bottom:calc(160px + env(safe-area-inset-bottom, 0px));z-index:40;width:52px;height:52px;border-radius:16px;border:0;cursor:pointer;
      background:linear-gradient(135deg,#10B981,#0d9488);color:#fff;font-size:22px;box-shadow:0 8px 24px rgba(16,185,129,.35);
      display:flex;align-items:center;justify-content:center;transition:transform .15s}
    .sa-fab:hover{transform:scale(1.06)} .sa-fab:active{transform:scale(.96)}
    @media (min-width:1024px){.sa-fab{right:24px;bottom:24px}}
    .sa-panel{position:fixed;z-index:60;left:8px;right:8px;bottom:calc(8px + env(safe-area-inset-bottom, 0px));height:min(78vh,620px);display:none;flex-direction:column;
      background:#fff;color:#0f172a;border:1px solid #e2e8f0;border-radius:20px;box-shadow:0 20px 50px rgba(15,23,42,.25);overflow:hidden;
      font-family:inherit;font-size:13px}
    @media (min-width:640px){.sa-panel{left:auto;right:24px;bottom:24px;width:380px;height:560px}}
    .sa-panel.sa-open{display:flex}
    .sa-head{display:flex;align-items:center;justify-content:space-between;padding:12px 14px;border-bottom:1px solid #e2e8f0}
    .sa-title{font-weight:700;font-size:14px} .sa-sub{font-size:11px;color:#64748b}
    .sa-icon-btn{background:none;border:0;cursor:pointer;color:#64748b;font-size:18px;line-height:1;padding:4px 6px;border-radius:8px}
    .sa-icon-btn:hover{background:#f1f5f9}
    .sa-body{flex:1;overflow-y:auto;padding:12px 14px;display:flex;flex-direction:column;gap:8px}
    .sa-msg{max-width:85%;padding:8px 11px;border-radius:14px;white-space:pre-wrap;word-wrap:break-word;line-height:1.45}
    .sa-user{align-self:flex-end;background:#10B981;color:#fff;border-bottom-right-radius:4px}
    .sa-bot{align-self:flex-start;background:#f1f5f9;color:#0f172a;border-bottom-left-radius:4px}
    .sa-err{align-self:flex-start;background:#fff1f2;color:#be123c}
    .sa-card{align-self:stretch;border:1px solid #a7f3d0;background:#ecfdf5;border-radius:14px;padding:10px 12px}
    .sa-card-title{font-size:10px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;color:#047857;margin-bottom:4px}
    .sa-card-actions{display:flex;gap:8px;margin-top:8px}
    .sa-card-summary{font-weight:700}
    .sa-card-details{margin:6px 0 0;padding-left:16px;color:#334155}
    .sa-card-details li{margin:2px 0}
    .sa-card.sa-danger{border-color:#fecdd3;background:#fff1f2}
    .sa-card.sa-danger .sa-card-title{color:#be123c}
    .sa-btn-danger{background:#e11d48;color:#fff} .sa-btn-danger:hover{background:#be123c}
    .sa-card.sa-stale{opacity:.55}
    .sa-btn{border:0;border-radius:10px;padding:6px 12px;font-weight:700;font-size:12px;cursor:pointer}
    .sa-btn-primary{background:#10B981;color:#fff} .sa-btn-primary:hover{background:#059669}
    .sa-btn-ghost{background:transparent;color:#475569;border:1px solid #cbd5e1}
    .sa-btn:disabled{opacity:.6;cursor:default}
    .sa-chips{display:flex;flex-wrap:wrap;gap:6px}
    .sa-chip{border:1px solid #e2e8f0;background:#fff;color:#334155;border-radius:999px;padding:5px 10px;font-size:12px;cursor:pointer}
    .sa-chip:hover{border-color:#10B981;color:#047857}
    .sa-foot{display:flex;gap:8px;padding:10px;border-top:1px solid #e2e8f0}
    .sa-input{flex:1;border:1px solid #cbd5e1;border-radius:12px;padding:8px 10px;font:inherit;background:#fff;color:inherit;resize:none;max-height:90px}
    .sa-input:focus{outline:2px solid #10B981;outline-offset:-1px}
    .sa-typing{align-self:flex-start;color:#64748b;font-size:12px;padding:2px 4px}
    html.dark .sa-panel{background:#0f172a;color:#e2e8f0;border-color:#1e293b}
    html.dark .sa-head,html.dark .sa-foot{border-color:#1e293b}
    html.dark .sa-icon-btn:hover{background:#1e293b}
    html.dark .sa-bot{background:#1e293b;color:#e2e8f0}
    html.dark .sa-err{background:#4c0519;color:#fecdd3}
    html.dark .sa-card{background:#022c22;border-color:#065f46}
    html.dark .sa-card-title{color:#6ee7b7}
    html.dark .sa-card-details{color:#cbd5e1}
    html.dark .sa-card.sa-danger{background:#4c0519;border-color:#881337}
    html.dark .sa-card.sa-danger .sa-card-title{color:#fda4af}
    html.dark .sa-chip{background:#0f172a;border-color:#334155;color:#cbd5e1}
    html.dark .sa-btn-ghost{color:#cbd5e1;border-color:#334155}
    html.dark .sa-input{background:#020617;border-color:#334155}`;

    function el(tag, cls, text) {
        const n = document.createElement(tag);
        if (cls) n.className = cls;
        if (text != null) n.textContent = text;
        return n;
    }

    function loadHistory() {
        try {
            const h = JSON.parse(sessionStorage.getItem(STORE_KEY) || '[]');
            return Array.isArray(h) ? h : [];
        } catch (e) { return []; }
    }
    function saveHistory() {
        try { sessionStorage.setItem(STORE_KEY, JSON.stringify(history.slice(-MAX_HISTORY))); } catch (e) { }
    }

    let history = loadHistory();
    let busy = false;
    let pendingCard = null; // only the newest draft can be confirmed
    let fab, panel, body, input, sendBtn;

    function scrollDown() { body.scrollTop = body.scrollHeight; }

    function renderMessage(m) {
        body.appendChild(el('div', 'sa-msg ' + (m.role === 'user' ? 'sa-user' : 'sa-bot'), m.content));
    }

    function renderWelcome() {
        body.appendChild(el('div', 'sa-msg sa-bot',
            "Hi! I can explain your spending, budgets, goals, debts and bills, and help you plan. I'll only change your records after you confirm each change."));
        const chips = el('div', 'sa-chips');
        SUGGESTIONS.forEach(s => {
            const c = el('button', 'sa-chip', s);
            c.type = 'button';
            c.addEventListener('click', () => send(s));
            chips.appendChild(c);
        });
        body.appendChild(chips);
    }

    function renderAll() {
        body.replaceChildren();
        renderWelcome();
        history.forEach(m => {
            if (m.role === 'assistant' && m.content.startsWith('[')) return;
            renderMessage({ role: m.role, content: m.content.replace(/\n\[Draft awaiting[^\]]*\]$/, '') });
        });
        scrollDown();
    }

    function noteOutcome(text) {
        history.push({ role: 'assistant', content: text });
        saveHistory();
    }

    function renderActionCard(action) {
        if (pendingCard) {
            pendingCard.classList.add('sa-stale');
            const old = pendingCard.querySelector('.sa-card-actions');
            if (old) old.replaceChildren(el('span', 'sa-sub', 'Replaced by a newer draft.'));
        }
        const card = el('div', 'sa-card' + (action.danger ? ' sa-danger' : ''));
        pendingCard = card;
        card.appendChild(el('div', 'sa-card-title', action.danger ? 'Please review carefully' : 'Confirm change'));
        card.appendChild(el('div', 'sa-card-summary', action.summary));
        if (Array.isArray(action.details) && action.details.length) {
            const ul = el('ul', 'sa-card-details');
            action.details.forEach(d => ul.appendChild(el('li', null, d)));
            card.appendChild(ul);
        }
        const row = el('div', 'sa-card-actions');
        const confirmLabel = action.kind === 'delete_transaction' ? 'Delete' : 'Confirm';
        const ok = el('button', 'sa-btn ' + (action.danger ? 'sa-btn-danger' : 'sa-btn-primary'), confirmLabel);
        const no = el('button', 'sa-btn sa-btn-ghost', 'Cancel');
        ok.type = no.type = 'button';
        no.addEventListener('click', () => {
            if (pendingCard === card) pendingCard = null;
            row.replaceChildren(el('span', 'sa-sub', 'Cancelled. Nothing was changed.'));
            noteOutcome('[The user cancelled the draft: ' + action.summary + '. Nothing was changed.]');
        });
        ok.addEventListener('click', async () => {
            ok.disabled = no.disabled = true;
            ok.textContent = 'Saving…';
            try {
                const res = await fetch('/api/assistant/confirm', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
                    body: JSON.stringify(action),
                });
                const data = await res.json().catch(() => ({}));
                if (!res.ok) throw new Error(data.error || 'Not saved: something went wrong.');
                if (pendingCard === card) pendingCard = null;
                const done = el('span', 'sa-sub', '✓ Saved. ');
                const refresh = el('a', null, 'Refresh page');
                refresh.href = '#';
                refresh.style.cssText = 'color:#10B981;font-weight:700';
                refresh.addEventListener('click', e => { e.preventDefault(); location.reload(); });
                done.appendChild(refresh);
                row.replaceChildren(done);
                noteOutcome('[The app confirmed this change was saved: ' + action.summary + '.]');
                window.dispatchEvent(new CustomEvent('spendly:data-changed', { detail: action }));
            } catch (err) {
                ok.disabled = no.disabled = false;
                ok.textContent = confirmLabel;
                body.appendChild(el('div', 'sa-msg sa-err', err.message));
                scrollDown();
            }
        });
        row.append(ok, no);
        card.appendChild(row);
        body.appendChild(card);
    }

    async function send(text) {
        text = (text || '').trim();
        if (!text || busy) return;
        busy = true;
        sendBtn.disabled = true;
        input.value = '';

        const userMsg = { role: 'user', content: text };
        history.push(userMsg);
        renderMessage(userMsg);
        const typing = el('div', 'sa-typing', 'Thinking…');
        body.appendChild(typing);
        scrollDown();

        try {
            const res = await fetch('/api/assistant/chat', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
                body: JSON.stringify({ messages: history.slice(-MAX_HISTORY) }),
            });
            const data = await res.json().catch(() => ({}));
            typing.remove();
            if (res.status === 401) { location.href = '/login'; return; }
            if (!res.ok) throw new Error(data.error || 'The assistant is unavailable right now.');
            const botMsg = { role: 'assistant', content: data.reply || '…' };
            renderMessage(botMsg);
            speak(data.reply);
            // Keep the draft in the conversation so follow-ups like "make it 4,500" have context.
            if (data.action && data.action.summary) {
                botMsg.content += '\n[Draft awaiting the user\'s confirmation: ' + data.action.summary + ']';
                renderActionCard(data.action);
            }
            history.push(botMsg);
            saveHistory();
        } catch (err) {
            typing.remove();
            history.pop(); // don't resend a turn that never got an answer
            body.appendChild(el('div', 'sa-msg sa-err', err.message || 'Network error'));
        } finally {
            busy = false;
            sendBtn.disabled = false;
            scrollDown();
            input.focus();
        }
    }

    let languageLoaded = false;
    function open() {
        panel.classList.add('sa-open'); fab.style.display = 'none'; input.focus(); scrollDown();
        if (!languageLoaded) { languageLoaded = true; loadLanguage(); }
    }
    function close() {
        panel.classList.remove('sa-open'); fab.style.display = '';
        stopSpeaking();
        if (recognition) recognition.abort();
    }

    // ─── Voice (browser speech APIs) ───────────────────────────────────────
    // Speech recognition fills and sends the message box; speech synthesis reads
    // replies aloud. Typing always keeps working, and every limitation is stated.
    const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
    const TTS = 'speechSynthesis' in window ? window.speechSynthesis : null;
    const SPEECH_LANG = {
        en: 'en-US', fr: 'fr-FR', es: 'es-ES', pt: 'pt-BR', de: 'de-DE', ar: 'ar-SA', sw: 'sw-KE',
        zh: 'zh-CN', hi: 'hi-IN', ha: 'ha-NG', yo: 'yo-NG', ig: 'ig-NG', pcm: 'en-NG',
    };
    const VOICE_KEY = 'spendly_assistant_voice';
    let assistantLang = 'en';
    let languageNames = {};
    let recognition = null;
    let micBtn;

    function voicePrefs() {
        try { return Object.assign({ speak: false, voiceURI: '' }, JSON.parse(localStorage.getItem(VOICE_KEY) || '{}')); }
        catch (e) { return { speak: false, voiceURI: '' }; }
    }
    function saveVoicePrefs(p) {
        try { localStorage.setItem(VOICE_KEY, JSON.stringify(p)); } catch (e) { }
    }
    function langName() { return languageNames[assistantLang] || 'this language'; }

    // Voices installed on this device for the reply language (prefix match, e.g. "fr").
    function voicesForLang() {
        if (!TTS) return [];
        const prefix = SPEECH_LANG[assistantLang].split('-')[0].toLowerCase();
        return TTS.getVoices().filter(v => (v.lang || '').toLowerCase().startsWith(prefix));
    }
    // Label a voice's gender only when its name says so; never guess.
    function voiceLabel(v) {
        const g = /\bfemale\b/i.test(v.name) ? ' (female)' : /\bmale\b/i.test(v.name) ? ' (male)' : '';
        return v.name + g + ' · ' + v.lang;
    }

    function stopSpeaking() { if (TTS) TTS.cancel(); }

    function speak(text) {
        const prefs = voicePrefs();
        if (!prefs.speak || !TTS || !text) return;
        stopSpeaking();
        const u = new SpeechSynthesisUtterance(text);
        u.lang = SPEECH_LANG[assistantLang];
        const chosen = TTS.getVoices().find(v => v.voiceURI === prefs.voiceURI);
        if (chosen) u.voice = chosen;
        TTS.speak(u);
    }

    function setMicState(listening) {
        micBtn.textContent = listening ? '■' : '🎤';
        micBtn.setAttribute('aria-pressed', listening ? 'true' : 'false');
        micBtn.setAttribute('aria-label', listening ? 'Stop listening' : 'Speak your question');
        micBtn.classList.toggle('sa-mic-on', listening);
    }

    function voiceNotice(text) {
        body.appendChild(el('div', 'sa-msg sa-err', text));
        scrollDown();
    }

    function toggleListening() {
        if (recognition) { recognition.stop(); return; }
        if (!SR) {
            voiceNotice("Voice input isn't available in this browser (try Chrome, Edge or Safari). You can keep typing.");
            return;
        }
        stopSpeaking();
        const rec = new SR();
        recognition = rec;
        rec.lang = SPEECH_LANG[assistantLang];
        rec.interimResults = true;
        rec.maxAlternatives = 1;
        let finalText = '';
        rec.onresult = e => {
            let interim = '';
            for (let i = e.resultIndex; i < e.results.length; i++) {
                const t = e.results[i][0].transcript;
                if (e.results[i].isFinal) finalText += t; else interim += t;
            }
            input.value = (finalText + interim).trim();
        };
        rec.onerror = e => {
            const msgs = {
                'not-allowed': 'Microphone access is blocked. Allow it in your browser settings, or keep typing.',
                'service-not-allowed': 'Voice input is turned off in this browser. You can keep typing.',
                'language-not-supported': "Voice input doesn't support " + langName() + ' in this browser. You can type instead, or choose another language in 🧠.',
                'no-speech': "I didn't hear anything. Tap 🎤 to try again, or type your question.",
                'audio-capture': 'No microphone was found. You can keep typing.',
                'network': "Voice input couldn't reach the browser's speech service. You can keep typing.",
            };
            if (e.error !== 'aborted') voiceNotice(msgs[e.error] || 'Voice input stopped unexpectedly. You can keep typing.');
        };
        rec.onend = () => {
            recognition = null;
            setMicState(false);
            if (finalText.trim()) send(finalText.trim());
        };
        try {
            rec.start();
            setMicState(true);
        } catch (err) {
            recognition = null;
            voiceNotice('Voice input could not start. You can keep typing.');
        }
    }

    async function loadLanguage() {
        try {
            const s = await api('GET', '/api/assistant/settings');
            assistantLang = s.language;
            languageNames = s.languages;
        } catch (e) { }
    }

    function renderVoiceSettings(container) {
        container.appendChild(el('div', 'sa-section', 'Voice'));
        container.appendChild(el('div', 'sa-sub', SR
            ? "🎤 Voice input: available. It uses your browser's speech service (in Chrome, audio is processed by Google)."
            : "🎤 Voice input isn't available in this browser (try Chrome, Edge or Safari). Typing always works."));
        if (!TTS) {
            container.appendChild(el('div', 'sa-sub', "🔊 Spoken replies aren't supported in this browser."));
            return;
        }
        const prefs = voicePrefs();
        const speakRow = el('label', 'sa-check');
        const speakBox = el('input');
        speakBox.type = 'checkbox';
        speakBox.checked = prefs.speak;
        speakRow.append(speakBox, el('span', null, ' Read replies aloud'));
        container.appendChild(speakRow);

        const voiceSel = el('select', 'sa-input sa-select');
        voiceSel.setAttribute('aria-label', 'Voice');
        const note = el('div', 'sa-sub');
        const fill = () => {
            const own = voicesForLang();
            const list = own.length ? own : TTS.getVoices();
            voiceSel.replaceChildren();
            const def = el('option', null, 'Browser default');
            def.value = '';
            voiceSel.appendChild(def);
            list.forEach(v => {
                const o = el('option', null, voiceLabel(v));
                o.value = v.voiceURI;
                voiceSel.appendChild(o);
            });
            voiceSel.value = list.some(v => v.voiceURI === prefs.voiceURI) ? prefs.voiceURI : '';
            if (!TTS.getVoices().length) note.textContent = 'No voices are installed on this device yet.';
            else if (!own.length) note.textContent = 'This device has no ' + langName() + ' voice, so replies may be read with another voice. Showing all voices.';
            else note.textContent = own.length + ' ' + langName() + ' voice' + (own.length === 1 ? '' : 's') + ' on this device.';
        };
        fill();
        TTS.addEventListener('voiceschanged', fill, { once: true });

        const sample = el('button', 'sa-btn sa-btn-ghost', 'Play sample');
        sample.type = 'button';
        const save = () => {
            prefs.speak = speakBox.checked;
            prefs.voiceURI = voiceSel.value;
            saveVoicePrefs(prefs);
        };
        speakBox.addEventListener('change', () => { save(); if (!speakBox.checked) stopSpeaking(); });
        voiceSel.addEventListener('change', save);
        sample.addEventListener('click', () => {
            const was = prefs.speak;
            prefs.speak = true; saveVoicePrefs(prefs);
            speak("Hi, I'm your Spendly Assistant.");
            prefs.speak = was; saveVoicePrefs(prefs);
        });
        const row = el('div', 'sa-voice-row');
        row.append(voiceSel, sample);
        container.append(row, note);
    }

    // ─── Memory & language management view ───────────────────────────────
    let manageView, chatFoot;

    async function api(method, url, payload) {
        const res = await fetch(url, {
            method,
            headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
            body: payload ? JSON.stringify(payload) : undefined,
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data.error || 'Something went wrong');
        return data;
    }

    function showManage(on) {
        manageView.style.display = on ? 'flex' : 'none';
        body.style.display = on ? 'none' : 'flex';
        chatFoot.style.display = on ? 'none' : 'flex';
        if (on) renderManage();
        else input.focus();
    }

    async function renderManage() {
        manageView.replaceChildren(el('div', 'sa-sub', 'Loading…'));
        let settings, mems;
        try {
            [settings, mems] = await Promise.all([api('GET', '/api/assistant/settings'), api('GET', '/api/assistant/memories')]);
        } catch (err) {
            manageView.replaceChildren(el('div', 'sa-msg sa-err', err.message));
            return;
        }
        manageView.replaceChildren();
        const status = el('div', 'sa-sub');

        // Language
        manageView.appendChild(el('div', 'sa-section', 'Reply language'));
        const langSel = el('select', 'sa-input sa-select');
        langSel.setAttribute('aria-label', 'Reply language');
        Object.entries(settings.languages).sort((a, b) => a[1].localeCompare(b[1])).forEach(([code, name]) => {
            const o = el('option', null, name);
            o.value = code;
            langSel.appendChild(o);
        });
        langSel.value = settings.language;
        assistantLang = settings.language;
        languageNames = settings.languages;
        const voiceBox = el('div', 'sa-manage-group');
        langSel.addEventListener('change', async () => {
            try {
                await api('POST', '/api/assistant/settings', { language: langSel.value });
                assistantLang = langSel.value;
                status.textContent = '✓ The assistant will now reply in ' + settings.languages[langSel.value] + '.';
                voiceBox.replaceChildren();
                renderVoiceSettings(voiceBox); // voices and speech language follow the reply language
            } catch (err) { status.textContent = err.message; }
        });
        manageView.append(langSel, status);
        renderVoiceSettings(voiceBox);
        manageView.appendChild(voiceBox);

        // Memories
        manageView.appendChild(el('div', 'sa-section', 'What the assistant remembers (' + mems.memories.length + '/' + mems.limit + ')'));
        manageView.appendChild(el('div', 'sa-sub', "Only things you approved. Edit or delete anything; it never stores PINs, passwords or card numbers."));
        const list = el('div', 'sa-mem-list');
        if (!mems.memories.length) list.appendChild(el('div', 'sa-sub', 'Nothing saved yet.'));
        mems.memories.forEach(m => list.appendChild(memoryRow(m)));
        manageView.appendChild(list);

        // Add
        const addRow = el('form', 'sa-mem-add');
        const addInput = el('input', 'sa-input');
        addInput.maxLength = 300;
        addInput.placeholder = 'e.g. I prefer short answers';
        addInput.setAttribute('aria-label', 'New memory');
        const addBtn = el('button', 'sa-btn sa-btn-primary', 'Add');
        addBtn.type = 'submit';
        addRow.addEventListener('submit', async e => {
            e.preventDefault();
            try {
                await api('POST', '/api/assistant/memories', { content: addInput.value, kind: 'preference' });
                renderManage();
            } catch (err) { status.textContent = err.message; langSel.scrollIntoView({ block: 'nearest' }); }
        });
        addRow.append(addInput, addBtn);
        manageView.appendChild(addRow);

        if (mems.memories.length) {
            const clearAll = el('button', 'sa-btn sa-btn-ghost', 'Delete all memories');
            clearAll.type = 'button';
            clearAll.addEventListener('click', async () => {
                if (!window.confirm('Delete everything the assistant remembers? This can\'t be undone.')) return;
                try { await api('DELETE', '/api/assistant/memories'); renderManage(); } catch (err) { status.textContent = err.message; }
            });
            manageView.appendChild(clearAll);
        }
    }

    function memoryRow(m) {
        const row = el('div', 'sa-mem');
        const text = el('div', 'sa-mem-text', m.content);
        const meta = el('div', 'sa-sub', m.kind + ' · ' + m.updated_at);
        const btns = el('div', 'sa-mem-btns');
        const edit = el('button', 'sa-icon-btn', '✎');
        const del = el('button', 'sa-icon-btn', '🗑');
        edit.type = del.type = 'button';
        edit.setAttribute('aria-label', 'Edit memory');
        del.setAttribute('aria-label', 'Delete memory');
        del.addEventListener('click', async () => {
            try { await api('DELETE', '/api/assistant/memories/' + m.id); renderManage(); }
            catch (err) { meta.textContent = err.message; }
        });
        edit.addEventListener('click', () => {
            const box = el('input', 'sa-input');
            box.value = m.content;
            box.maxLength = 300;
            box.setAttribute('aria-label', 'Edit memory');
            const save = el('button', 'sa-btn sa-btn-primary', 'Save');
            const cancel = el('button', 'sa-btn sa-btn-ghost', 'Cancel');
            save.type = cancel.type = 'button';
            save.addEventListener('click', async () => {
                try { await api('PUT', '/api/assistant/memories/' + m.id, { content: box.value, kind: m.kind }); renderManage(); }
                catch (err) { meta.textContent = err.message; }
            });
            cancel.addEventListener('click', () => row.replaceWith(memoryRow(m)));
            row.replaceChildren(box, el('div', 'sa-card-actions'));
            row.lastChild.append(save, cancel, meta);
            box.focus();
        });
        btns.append(edit, del);
        const left = el('div');
        left.style.flex = '1';
        left.append(text, meta);
        row.append(left, btns);
        return row;
    }

    function build() {
        document.head.appendChild(el('style', null, css + `
    .sa-manage{flex:1;overflow-y:auto;padding:12px 14px;display:none;flex-direction:column;gap:8px}
    .sa-section{font-weight:700;font-size:12px;margin-top:6px}
    .sa-select{flex:none;width:100%}
    .sa-mem-list{display:flex;flex-direction:column;gap:6px}
    .sa-mem{display:flex;gap:8px;align-items:flex-start;border:1px solid #e2e8f0;border-radius:12px;padding:8px 10px}
    .sa-mem-text{white-space:pre-wrap;word-wrap:break-word}
    .sa-mem-btns{display:flex;gap:2px}
    .sa-mem-add{display:flex;gap:8px}
    .sa-manage-group{display:flex;flex-direction:column;gap:8px}
    .sa-voice-row{display:flex;gap:8px;align-items:center}
    .sa-voice-row .sa-select{flex:1;width:auto;min-width:0}
    .sa-voice-row .sa-btn{flex:none;white-space:nowrap}
    .sa-check{display:flex;align-items:center;gap:6px;cursor:pointer}
    .sa-mic{padding:6px 10px}
    .sa-mic-on{background:#e11d48;border-color:#e11d48;color:#fff}
    html.dark .sa-mem{border-color:#334155}`));

        fab = el('button', 'sa-fab', '✨');
        fab.type = 'button';
        fab.title = 'Ask Spendly Assistant';
        fab.setAttribute('aria-label', 'Open Spendly Assistant');
        fab.addEventListener('click', open);

        panel = el('div', 'sa-panel');
        panel.setAttribute('role', 'dialog');
        panel.setAttribute('aria-label', 'Spendly Assistant');

        const head = el('div', 'sa-head');
        const titles = el('div');
        titles.append(el('div', 'sa-title', '✨ Spendly Assistant'), el('div', 'sa-sub', 'Answers from your own data'));
        const actions = el('div');
        const clearBtn = el('button', 'sa-icon-btn', '⟲');
        clearBtn.type = 'button';
        clearBtn.title = 'New conversation';
        clearBtn.setAttribute('aria-label', 'New conversation');
        clearBtn.addEventListener('click', () => { history = []; saveHistory(); stopSpeaking(); renderAll(); showManage(false); });
        const closeBtn = el('button', 'sa-icon-btn', '×');
        closeBtn.type = 'button';
        closeBtn.setAttribute('aria-label', 'Close assistant');
        closeBtn.addEventListener('click', close);
        const memBtn = el('button', 'sa-icon-btn', '🧠');
        memBtn.type = 'button';
        memBtn.title = 'Memory, language & voice';
        memBtn.setAttribute('aria-label', 'Memory, language and voice settings');
        memBtn.addEventListener('click', () => showManage(manageView.style.display !== 'flex'));
        actions.append(memBtn, clearBtn, closeBtn);
        head.append(titles, actions);

        body = el('div', 'sa-body');
        body.setAttribute('aria-live', 'polite');

        const foot = el('form', 'sa-foot');
        input = el('textarea', 'sa-input');
        input.rows = 1;
        input.maxLength = 1000;
        input.placeholder = 'Ask about your money…';
        input.setAttribute('aria-label', 'Message');
        input.addEventListener('keydown', e => {
            if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(input.value); }
        });
        sendBtn = el('button', 'sa-btn sa-btn-primary', 'Send');
        sendBtn.type = 'submit';
        foot.addEventListener('submit', e => { e.preventDefault(); send(input.value); });
        micBtn = el('button', 'sa-btn sa-btn-ghost sa-mic', '🎤');
        micBtn.type = 'button';
        micBtn.title = SR ? 'Speak your question' : "Voice input isn't available in this browser";
        micBtn.setAttribute('aria-label', 'Speak your question');
        micBtn.setAttribute('aria-pressed', 'false');
        micBtn.addEventListener('click', toggleListening);
        foot.append(input, micBtn, sendBtn);
        chatFoot = foot;
        manageView = el('div', 'sa-manage');

        panel.append(head, body, manageView, foot);
        document.body.append(fab, panel);
        document.addEventListener('keydown', e => {
            if (e.key === 'Escape' && panel.classList.contains('sa-open')) close();
        });
        renderAll();
    }

    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', build);
    else build();
})();
