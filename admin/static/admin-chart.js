// Draws the sign-ups chart on the dashboard. Chart.js is embedded next to this
// file, so the panel needs no CDN. The data comes from attributes on the
// canvas (no inline script), and a table inside the canvas is the fallback for
// screen readers.
(function () {
	var chart = null;

	function cssVar(name) {
		return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
	}

	function draw() {
		var canvas = document.querySelector('canvas[data-chart="signups"]');
		if (!canvas || typeof Chart === 'undefined') return;

		var labels, values;
		try {
			labels = JSON.parse(canvas.getAttribute('data-labels') || '[]');
			values = JSON.parse(canvas.getAttribute('data-values') || '[]');
		} catch (e) {
			return;
		}

		// Redrawing (theme change) must destroy the old chart first, or Chart.js
		// refuses to reuse the canvas.
		if (chart) chart.destroy();

		var primary = cssVar('--color-primary');
		var muted = cssVar('--muted');
		var grid = cssVar('--color-base-300');
		var reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

		chart = new Chart(canvas, {
			type: 'bar',
			data: {
				labels: labels,
				datasets: [{ label: 'New sign-ups', data: values, backgroundColor: primary, borderRadius: 4, maxBarThickness: 28 }]
			},
			options: {
				responsive: true,
				maintainAspectRatio: false,
				animation: reduceMotion ? false : { duration: 300 },
				plugins: { legend: { display: false } },
				scales: {
					x: { ticks: { color: muted, maxRotation: 0, autoSkip: true, maxTicksLimit: 7 }, grid: { display: false } },
					y: { beginAtZero: true, ticks: { color: muted, precision: 0 }, grid: { color: grid } }
				}
			}
		});
	}

	function init() {
		draw();
		// Redraw with the new colors when the theme is switched.
		new MutationObserver(draw).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
	}

	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', init);
	} else {
		init();
	}
})();
