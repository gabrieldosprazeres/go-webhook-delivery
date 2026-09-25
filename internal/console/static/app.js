(() => {
  'use strict';

  const toggle = document.querySelector('#metrics-refresh-toggle');
  const status = document.querySelector('#metrics-status');
  if (!toggle || !status) return;

  let paused = false;
  let refreshing = false;

  const setMetric = (name, value, suffix = '') => {
    document.querySelectorAll(`[data-metric="${name}"]`).forEach((element) => {
      element.textContent = `${value}${suffix}`;
    });
  };

  const refresh = async () => {
    if (paused || refreshing || document.hidden) return;
    refreshing = true;
    try {
      const response = await fetch('/app/metrics', {
        credentials: 'same-origin',
        headers: { Accept: 'application/json' },
      });
      if (!response.ok || !response.headers.get('content-type')?.startsWith('application/json')) {
        throw new Error('metrics unavailable');
      }
      const snapshot = await response.json();
      const summary = snapshot.summary;
      for (const name of [
        'deliveries', 'succeeded', 'retry_scheduled', 'dead_letter', 'pending',
        'processing', 'attempts', 'retry_attempts', 'failed_permanent',
      ]) setMetric(name, summary[name]);
      setMetric('p95_duration_ms', Math.round(summary.p95_duration_ms), ' ms');

      document.querySelectorAll('.bars span').forEach((bar, index) => {
        const point = snapshot.series[index];
        if (!point) return;
        bar.className = point.deliveries > 0 ? 'active' : 'idle';
        bar.title = `${new Date(point.bucket).toLocaleString('pt-BR')} · ${point.deliveries} entregas`;
      });
      status.textContent = `Métricas atualizadas às ${new Date(snapshot.summary.generated_at).toLocaleTimeString('pt-BR')}.`;
    } catch {
      status.textContent = 'Não foi possível atualizar as métricas agora.';
    } finally {
      refreshing = false;
    }
  };

  toggle.hidden = false;
  toggle.addEventListener('click', () => {
    paused = !paused;
    toggle.textContent = paused ? 'Retomar atualizações' : 'Pausar atualizações';
    status.textContent = paused ? 'Atualizações automáticas pausadas.' : 'Atualizações automáticas retomadas.';
    if (!paused) void refresh();
  });
  window.setInterval(() => void refresh(), 30_000);
})();
