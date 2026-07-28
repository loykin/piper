import type { RunMetrics, RunMetricValues } from './types'

export function groupRunMetrics(metrics: RunMetrics): RunMetricValues {
  const grouped: RunMetricValues = {}
  for (const metric of metrics) {
    if (typeof metric.value !== 'number' || !Number.isFinite(metric.value)) continue
    grouped[metric.step_name] ??= {}
    grouped[metric.step_name][metric.key] = metric.value
  }
  return grouped
}
