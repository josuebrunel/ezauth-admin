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

	// Let htmx swap error responses too: the server answers failed actions with
	// a toast (out-of-band) and the real status code.
	document.addEventListener('htmx:beforeSwap', function (e) {
		if (e.detail.xhr.status >= 400) {
			e.detail.shouldSwap = true;
			e.detail.isError = false;
		}
	});
})();
