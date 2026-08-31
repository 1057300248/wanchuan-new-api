import type { EndpointDoc, GuideDoc } from '../content/types'

export function getDocsOrigin(): string {
  if (typeof window === 'undefined') {
    return ''
  }
  return window.location.origin
}

/** 把内容中的 {{BASE_URL}} / {{WS_BASE_URL}} 占位符替换为当前站点 origin */
export function applyBaseUrl(text: string, origin = getDocsOrigin()): string {
  return text
    .replaceAll('{{BASE_URL}}', origin)
    .replaceAll('{{WS_BASE_URL}}', origin.replace(/^http/, 'ws'))
}

/** 生成指南的完整 Markdown（用于「复制 Markdown」按钮） */
export function buildGuideMarkdown(
  guide: GuideDoc,
  origin = getDocsOrigin()
): string {
  const lines = [
    `# ${guide.title}`,
    '',
    `> ${guide.description}`,
    '',
    applyBaseUrl(guide.body, origin),
    '',
    `---`,
    '',
    `来源：${origin}/help/guides/${guide.slug}`,
  ]
  return lines.join('\n')
}

/** 生成端点文档的完整 Markdown（用于 API 参考页「复制 Markdown」按钮） */
export function buildEndpointMarkdown(
  endpoint: EndpointDoc,
  sampleCurl: string,
  origin = getDocsOrigin()
): string {
  const okResponse = endpoint.responses.find((item) => item.status === 200)
  const lines = [
    `# ${endpoint.method} ${endpoint.path} — ${endpoint.name}`,
    '',
    endpoint.description,
    '',
    `- Base URL: ${origin}`,
    `- 认证：Authorization: Bearer <API_TOKEN>`,
    `- 中继格式：${endpoint.relayFormat}`,
    `- 响应类型：${endpoint.responseType}`,
    '',
    '## 请求示例',
    '',
    '```bash',
    applyBaseUrl(sampleCurl, origin),
    '```',
  ]
  if (okResponse) {
    lines.push('', '## 响应示例（200）', '', '```json', okResponse.body, '```')
  }
  if (endpoint.notes.length > 0) {
    lines.push('', '## 备注', '')
    for (const note of endpoint.notes) {
      lines.push(`- ${note}`)
    }
  }
  lines.push('', '---', '', `来源：${origin}/help/api`)
  return lines.join('\n')
}
