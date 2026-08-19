type Level = 'info' | 'warn' | 'error'

/**
 * One JSON object per line on stdout. The cluster's Loki pipeline scrapes
 * container stdout, so this format is what makes chat queryable in Grafana —
 * the Bedrock server itself never prints chat, and no server.properties
 * setting exists that would make it.
 */
export function log(level: Level, event: string, fields: Record<string, unknown> = {}): void {
  const line = JSON.stringify({
    ts: new Date().toISOString(),
    level,
    event,
    ...fields,
  })
  if (level === 'error') {
    process.stderr.write(`${line}\n`)
  } else {
    process.stdout.write(`${line}\n`)
  }
}
