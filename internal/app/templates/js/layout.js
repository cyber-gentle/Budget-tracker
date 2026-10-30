// Shared behaviour for the page chrome in layout.html (header controls,
// profile menu and modal, mobile "More" sheet). Loaded on every signed-in page
// except the dashboard, which still has its own header script.
(function initSpendlyHeader() {
    const themeToggleBtn = document.getElementById('theme-toggle-btn');
    const themeToggleDropdownBtn = document.getElementById('btn-toggle-theme-dropdown');
    const themeIconSun = document.getElementById('theme-icon-sun');
    const themeIconMoon = document.getElementById('theme-icon-moon');
    const themeToggleText = document.getElementById('theme-toggle-text');
    const themeLabelDropdown = document.getElementById('theme-label-dropdown');

    function updateThemeUI(isDark) {
        if (isDark) {
            document.documentElement.classList.add('dark');
            if (themeIconSun) themeIconSun.classList.remove('hidden');
            if (themeIconMoon) themeIconMoon.classList.add('hidden');
            if (themeToggleText) themeToggleText.textContent = 'Dark';
            if (themeLabelDropdown) themeLabelDropdown.textContent = 'Dark';
        } else {
            document.documentElement.classList.remove('dark');
            if (themeIconSun) themeIconSun.classList.add('hidden');
            if (themeIconMoon) themeIconMoon.classList.remove('hidden');
            if (themeToggleText) themeToggleText.textContent = 'Light';
            if (themeLabelDropdown) themeLabelDropdown.textContent = 'Light';
        }
        try {
            if (typeof refreshChartThemes === 'function') refreshChartThemes(isDark);
            if (typeof window.currentReport !== 'undefined' && typeof renderCashFlowChart === 'function' && window.currentReport) {
                renderCashFlowChart(window.currentReport.timeline);
            }
            if (typeof window.chartInstance !== 'undefined' && typeof window.netWorthData !== 'undefined' && typeof renderChart === 'function' && window.chartInstance && window.netWorthData) {
                renderChart(window.netWorthData.trend);
            }
            if (typeof window.currentForecast !== 'undefined' && typeof drawChart === 'function' && window.currentForecast) {
                drawChart(window.currentForecast);
            }
        } catch (e) {}
    }

    function toggleTheme() {
        const currentDark = document.documentElement.classList.contains('dark');
        const newDark = !currentDark;
        try {
            localStorage.setItem('spendly-theme', newDark ? 'dark' : 'light');
            localStorage.setItem('spendly_theme', newDark ? 'dark' : 'light');
        } catch (e) {}
        updateThemeUI(newDark);
    }

    if (themeToggleBtn) {
        themeToggleBtn.addEventListener('click', (e) => {
            e.preventDefault();
            toggleTheme();
        });
    }
    if (themeToggleDropdownBtn) {
        themeToggleDropdownBtn.addEventListener('click', (e) => {
            e.preventDefault();
            e.stopPropagation();
            toggleTheme();
        });
    }

    // Sync theme on page load
    updateThemeUI(document.documentElement.classList.contains('dark'));

    // ─── User Profile Menu Dropdown ──────────────────────────────────────────
    const btnUserMenu = document.getElementById('btn-user-menu');
    const userDropdownMenu = document.getElementById('user-dropdown-menu');
    if (btnUserMenu && userDropdownMenu) {
        btnUserMenu.addEventListener('click', (e) => {
            e.preventDefault();
            e.stopPropagation();
            userDropdownMenu.classList.toggle('hidden');
        });
        document.addEventListener('click', (e) => {
            if (!userDropdownMenu.contains(e.target) && !btnUserMenu.contains(e.target)) {
                userDropdownMenu.classList.add('hidden');
            }
        });
    }

    // ─── Header & Dropdown Actions: Export, Categories, Profile ──────────────
    function handleExportAction(e) {
        e.preventDefault();
        if (userDropdownMenu) userDropdownMenu.classList.add('hidden');
        const exportModal = document.getElementById('export-modal');
        if (exportModal) {
            exportModal.classList.remove('hidden');
        } else if (typeof openExportModal === 'function') {
            openExportModal();
        } else {
            window.location.href = '/dashboard?action=export';
        }
    }
    const btnExportHeader = document.getElementById('btn-export-header');
    const btnDropdownExport = document.getElementById('btn-dropdown-export');
    if (btnExportHeader) btnExportHeader.addEventListener('click', handleExportAction);
    if (btnDropdownExport) btnDropdownExport.addEventListener('click', handleExportAction);

    const btnOpenCategoryMenu = document.getElementById('btn-open-category-modal-menu');
    if (btnOpenCategoryMenu) {
        btnOpenCategoryMenu.addEventListener('click', (e) => {
            e.preventDefault();
            if (userDropdownMenu) userDropdownMenu.classList.add('hidden');
            const catModal = document.getElementById('category-modal');
            if (catModal) {
                catModal.classList.remove('hidden');
            } else if (typeof openCategoryModal === 'function') {
                openCategoryModal('expense');
            } else {
                window.location.href = '/dashboard?action=categories';
            }
        });
    }

    // Profile Modal Controller
    const btnOpenProfileModal = document.getElementById('btn-open-profile-modal');
    const profileModal = document.getElementById('profile-modal');
    const profileModalClose = document.getElementById('profile-modal-close');
    const profileCancelBtn = document.getElementById('profile-cancel-btn');
    const profileForm = document.getElementById('profile-form');
    const profileError = document.getElementById('profile-error');
    const profileSubmitBtn = document.getElementById('profile-submit-btn');
    const btnTogglePass = document.getElementById('btn-toggle-password-fields');
    const passFieldsContainer = document.getElementById('password-fields-container');
    const passToggleChevron = document.getElementById('password-toggle-chevron');

    function safeToast(msg, type = 'success') {
        if (typeof showToast === 'function') {
            showToast(msg, type);
        } else {
            const t = document.createElement('div');
            t.className = `fixed bottom-5 right-5 z-50 px-4 py-2.5 rounded-xl text-xs font-semibold shadow-lg text-white ${type === 'error' ? 'bg-rose-600' : 'bg-slate-900 dark:bg-emerald-600'} transition-all`;
            t.textContent = msg;
            document.body.appendChild(t);
            setTimeout(() => t.remove(), 3000);
        }
    }

    if (btnOpenProfileModal) {
        btnOpenProfileModal.addEventListener('click', async (e) => {
            e.preventDefault();
            if (userDropdownMenu) userDropdownMenu.classList.add('hidden');
            if (profileModal) {
                if (profileError) profileError.classList.add('hidden');
                const curPass = document.getElementById('profile-current-pass');
                const newPass = document.getElementById('profile-new-pass');
                const confPass = document.getElementById('profile-confirm-pass');
                if (curPass) curPass.value = '';
                if (newPass) newPass.value = '';
                if (confPass) confPass.value = '';
                if (passFieldsContainer) passFieldsContainer.classList.add('hidden');
                if (passToggleChevron) passToggleChevron.classList.remove('rotate-180');

                try {
                    const res = await fetch('/api/profile');
                    if (res.ok) {
                        const prof = await res.json();
                        const fn = document.getElementById('profile-fullname');
                        const em = document.getElementById('profile-email');
                        const cur = document.getElementById('profile-currency');
                        if (fn) fn.value = prof.full_name || '';
                        if (em) em.value = prof.email || '';
                        if (cur && prof.currency) cur.value = prof.currency;
                    }
                } catch (err) {
                    console.error('Error fetching profile:', err);
                }
                profileModal.classList.remove('hidden');
            } else {
                window.location.href = '/dashboard?action=profile';
            }
        });
    }

    function closeProfileModal() {
        if (profileModal) profileModal.classList.add('hidden');
        if (profileError) profileError.classList.add('hidden');
    }

    if (profileModalClose) profileModalClose.addEventListener('click', closeProfileModal);
    if (profileCancelBtn) profileCancelBtn.addEventListener('click', closeProfileModal);

    if (btnTogglePass && passFieldsContainer) {
        btnTogglePass.addEventListener('click', () => {
            passFieldsContainer.classList.toggle('hidden');
            if (passToggleChevron) passToggleChevron.classList.toggle('rotate-180');
        });
    }

    if (profileForm) {
        profileForm.addEventListener('submit', async function (e) {
            e.preventDefault();
            if (profileError) profileError.classList.add('hidden');

            const fnEl = document.getElementById('profile-fullname');
            const emEl = document.getElementById('profile-email');
            const curEl = document.getElementById('profile-currency');
            const curPassEl = document.getElementById('profile-current-pass');
            const newPassEl = document.getElementById('profile-new-pass');
            const confPassEl = document.getElementById('profile-confirm-pass');

            const fullName = fnEl ? fnEl.value.trim() : '';
            const email = emEl ? emEl.value.trim() : '';
            const currency = curEl ? curEl.value : '';
            const currentPass = curPassEl ? curPassEl.value : '';
            const newPass = newPassEl ? newPassEl.value : '';
            const confirmPass = confPassEl ? confPassEl.value : '';

            if (newPass) {
                if (newPass.length < 6) {
                    if (profileError) {
                        profileError.textContent = 'New password must be at least 6 characters.';
                        profileError.classList.remove('hidden');
                    }
                    return;
                }
                if (!currentPass) {
                    if (profileError) {
                        profileError.textContent = 'Please enter your current password to set a new password.';
                        profileError.classList.remove('hidden');
                    }
                    return;
                }
                if (newPass !== confirmPass) {
                    if (profileError) {
                        profileError.textContent = 'New passwords do not match.';
                        profileError.classList.remove('hidden');
                    }
                    return;
                }
            }

            if (profileSubmitBtn) {
                profileSubmitBtn.disabled = true;
                profileSubmitBtn.innerHTML = '<span>Saving...</span>';
            }

            try {
                const fd = new FormData();
                fd.set('full_name', fullName);
                fd.set('email', email);
                if (currency) fd.set('currency', currency);
                if (newPass) {
                    fd.set('current_password', currentPass);
                    fd.set('new_password', newPass);
                }

                const res = await fetch('/api/profile', {
                    method: 'POST',
                    body: fd
                });

                const data = await res.json();
                if (!res.ok) {
                    if (profileError) {
                        profileError.textContent = data.error || 'Failed to update profile.';
                        profileError.classList.remove('hidden');
                    }
                } else {
                    const badge = document.getElementById('user-avatar-badge');
                    const nameEl = document.getElementById('user-display-name');
                    const dropFullname = document.getElementById('dropdown-fullname');
                    const dropEmail = document.getElementById('dropdown-email');
                    if (badge && data.initials) badge.textContent = data.initials;
                    if (nameEl && data.full_name) nameEl.textContent = data.full_name;
                    if (dropFullname && data.full_name) dropFullname.textContent = data.full_name;
                    if (dropEmail && data.email) dropEmail.textContent = data.email;

                    if (data.currency) {
                        const currSelect = document.getElementById('currency-select');
                        if (currSelect) currSelect.value = data.currency;
                        document.querySelectorAll('.currency-label').forEach(el => el.textContent = data.currency);
                    }

                    closeProfileModal();
                    safeToast('Profile updated successfully!');
                }
            } catch (err) {
                console.error('Error updating profile:', err);
                if (profileError) {
                    profileError.textContent = 'A network error occurred. Please try again.';
                    profileError.classList.remove('hidden');
                }
            } finally {
                if (profileSubmitBtn) {
                    profileSubmitBtn.disabled = false;
                    profileSubmitBtn.innerHTML = '<span>Save Changes</span>';
                }
            }
        });
    }
})();

function initMobileMoreSheet() {
    const navMobileMore = document.getElementById('btn-nav-mobile-more');
    const mobileMoreSheet = document.getElementById('mobile-more-sheet');
    const btnCloseMoreSheet = document.getElementById('btn-close-more-sheet');

    if (navMobileMore && mobileMoreSheet) {
        navMobileMore.addEventListener('click', (e) => {
            e.preventDefault();
            mobileMoreSheet.classList.remove('hidden');
        });
    }
    if (btnCloseMoreSheet && mobileMoreSheet) {
        btnCloseMoreSheet.addEventListener('click', (e) => {
            e.preventDefault();
            mobileMoreSheet.classList.add('hidden');
        });
    }
    if (mobileMoreSheet) {
        mobileMoreSheet.addEventListener('click', (e) => {
            if (e.target === mobileMoreSheet) mobileMoreSheet.classList.add('hidden');
        });
    }
}

// Only bind the shared sheet; pages with their own sheet handle it themselves.
if (document.querySelector('#mobile-more-sheet[data-shared-sheet]')) {
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initMobileMoreSheet);
    } else {
        initMobileMoreSheet();
    }
}
