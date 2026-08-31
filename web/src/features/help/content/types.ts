export interface GuideMeta {
  slug: string
  /** 中文标题（文档内容为简体中文单语） */
  title: string
  description: string
  recommended?: boolean
  /** 最近校订日（绝对日期，如 2026-08-30） */
  updated?: string
}

export interface GuideDoc extends GuideMeta {
  /**
   * Markdown 正文。`{{BASE_URL}}` 会在渲染与复制时替换为当前站点 origin，
   * 例如 https://chuanapi.com。
   */
  body: string
}

export interface GuideGroup {
  id: string
  title: string
  slugs: readonly string[]
}

export type HttpMethod = 'GET' | 'POST'

export interface EndpointResponseExample {
  status: number
  label: string
  description: string
  body: string
}

export interface EndpointDoc {
  id: string
  category: string
  name: string
  method: HttpMethod
  path: string
  description: string
  /** 中继格式说明，如「原生 OpenAI 格式」 */
  relayFormat: string
  responseType: string
  /** 请求体形态；默认无请求体 */
  requestKind?: 'json' | 'multipart' | 'websocket'
  /** 鉴权头；默认 Authorization: Bearer */
  authHeader?: 'Authorization' | 'x-api-key' | 'x-goog-api-key'
  /** 除鉴权与 Content-Type 外的固定请求头 */
  extraHeaders?: Readonly<Record<string, string>>
  /** 网关处理备注 */
  notes: readonly string[]
  /** JSON 请求体示例（美化后的字符串）；无请求体的端点留空 */
  requestBody?: string
  /** multipart/form-data 字段；file=true 时 value 是示例文件名 */
  multipartFields?: readonly {
    name: string
    value: string
    file?: boolean
  }[]
  /** 覆盖自动生成的示例代码（键为语言 id） */
  sampleOverrides?: Partial<Record<SampleLanguage, string>>
  responses: readonly EndpointResponseExample[]
}

export interface EndpointCategory {
  id: string
  title: string
  endpoints: readonly EndpointDoc[]
}

export type SampleLanguage = 'curl' | 'javascript' | 'python' | 'go'
