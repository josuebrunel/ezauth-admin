// Loaded in <head> so the saved theme applies before first paint.
(function () {
	var KEY = 'ezadmin-theme';
	var root = document.documentElement;

	function stored() {
		try { return localStorage.getItem(KEY); } catch (e) { return null; }
	}
	function apply(theme) {
		root.setAttribute('data-theme', theme);
		var btn = document.querySelector('[data-theme-toggle]');
		if (btn) btn.setAttribute('aria-pressed', theme === 'ezdark' ? 'true' : 'false');
	}

	var theme = stored() || (matchMedia('(prefers-color-scheme: dark)').matches ? 'ezdark' : 'ezlight');
	apply(theme);

	document.addEventListener('DOMContentLoaded', function () { apply(root.getAttribute('data-theme')); });
	document.addEventListener('click', function (e) {
		if (!e.target.closest('[data-theme-toggle]')) return;
		var next = root.getAttribute('data-theme') === 'ezdark' ? 'ezlight' : 'ezdark';
		apply(next);
		try { localStorage.setItem(KEY, next); } catch (err) { /* private mode: theme just won't persist */ }
	});

	// Native <dialog> helpers: open one from a button, and keep a destructive
	// confirm button disabled until the typed text matches. The server checks
	// the match again, so this only saves a round trip.
	document.addEventListener('click', function (e) {
		var opener = e.target.closest('[data-dialog-open]');
		if (!opener) return;
		var dlg = document.getElementById(opener.getAttribute('data-dialog-open'));
		if (dlg && dlg.showModal) dlg.showModal();
	});
	document.addEventListener('input', function (e) {
		var input = e.target.closest('[data-confirm-input]');
		if (!input) return;
		var btn = document.querySelector(input.getAttribute('data-confirm-button'));
		if (btn) {
			btn.disabled = input.value.trim().toLowerCase() !== input.getAttribute('data-confirm-value').toLowerCase();
		}
	});

	// A read-only field that selects its whole value when focused, and a button
	// that copies another field's value to the clipboard (the invitation link).
	document.addEventListener('focusin', function (e) {
		if (e.target.matches && e.target.matches('[data-select-on-focus]')) e.target.select();
	});
	document.addEventListener('click', function (e) {
		var btn = e.target.closest('[data-copy-from]');
		if (!btn) return;
		var field = document.querySelector(btn.getAttribute('data-copy-from'));
		if (!field) return;
		var label = btn.textContent;
		function done(ok) {
			btn.textContent = ok ? 'Copied' : 'Press Ctrl+C';
			setTimeout(function () { btn.textContent = label; }, 2000);
		}
		field.select();
		if (navigator.clipboard && navigator.clipboard.writeText) {
			navigator.clipboard.writeText(field.value).then(function () { done(true); }, function () { done(false); });
		} else {
			done(false);
		}
	});

	// After a boosted navigation, close the mobile menu and move focus to the
	// new content so keyboard and screen reader users land on it.
	document.addEventListener('htmx:afterSwap', function (e) {
		if (!e.detail.boosted) return;
		var drawer = document.getElementById('nav-drawer');
		if (drawer) drawer.checked = false;
		var main = document.getElementById('main');
		if (main) { main.setAttribute('tabindex', '-1'); main.focus({ preventScroll: true }); }
	});

	// Let htmx swap error responses too: the server answers failed actions with
	// a toast (out-of-band) and the real status code.
	document.addEventListener('htmx:beforeSwap', function (e) {
		if (e.detail.xhr.status >= 400) {
			e.detail.shouldSwap = true;
			e.detail.isError = false;
		}
	});
})();
