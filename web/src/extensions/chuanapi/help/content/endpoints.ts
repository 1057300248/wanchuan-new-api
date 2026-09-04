/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type {
  EndpointCategory,
  EndpointDoc,
  EndpointResponseExample,
} from './types'

function json(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

const badRequest: EndpointResponseExample = {
  status: 400,
  label: '请求参数错误',
  description: '请求体、路径参数或模型参数不符合当前端点要求。',
  body: json({
    error: {
      message: 'Invalid request body',
      type: 'invalid_request_error',
      code: 'invalid_request',
    },
  }),
}

const rateLimited: EndpointResponseExample = {
  status: 429,
  label: '请求过于频繁',
  description: '触发令牌、模型或系统限速；等待后按 Retry-After 重试。',
  body: json({
    error: {
      message: 'Rate limit reached. Retry later.',
      type: 'rate_limit_error',
      code: 'rate_limit_exceeded',
    },
  }),
}

function responses(
  label: string,
  description: string,
  body: unknown,
  status = 200
): readonly EndpointResponseExample[] {
  return [
    {
      status,
      label,
      description,
      body: typeof body === 'string' ? body : json(body),
    },
    badRequest,
    rateLimited,
  ]
}

const commonAvailabilityNote =
  '实际可用性取决于公开模型目录、令牌分组与对应上游能力；请先在定价页确认模型 id。'

export const endpointCategories: readonly EndpointCategory[] = [
  {
    id: 'models',
    title: '模型列表',
    endpoints: [
      {
        id: 'models-list',
        category: 'models',
        name: '列出模型',
        method: 'GET',
        path: '/v1/models',
        description: '列出当前令牌分组可访问的模型目录。',
        relayFormat: 'OpenAI / Anthropic / Gemini 模型列表兼容格式',
        responseType: 'application/json',
        notes: [
          '响应会按令牌分组过滤，不保证与未登录定价目录完全相同。',
          commonAvailabilityNote,
        ],
        responses: responses('模型列表', '返回可用模型对象数组。', {
          object: 'list',
          data: [
            {
              id: 'MODEL_ID',
              object: 'model',
              owned_by: 'provider',
            },
          ],
        }),
      },
      {
        id: 'models-retrieve',
        category: 'models',
        name: '获取模型',
        method: 'GET',
        path: '/v1/models/{model}',
        description: '按真实 model id 获取单个模型的公开信息。',
        relayFormat: 'OpenAI / Anthropic 模型详情兼容格式',
        responseType: 'application/json',
        notes: [
          '把路径中的 {model} 替换为定价页显示的完整 model id。',
          commonAvailabilityNote,
        ],
        responses: responses('模型详情', '返回单个模型对象。', {
          id: 'MODEL_ID',
          object: 'model',
          owned_by: 'provider',
        }),
      },
    ],
  },
  {
    id: 'chat',
    title: '聊天与生成',
    endpoints: [
      {
        id: 'chat-completions',
        category: 'chat',
        name: '聊天完成',
        method: 'POST',
        path: '/v1/chat/completions',
        description:
          '根据对话历史生成回复，兼容 OpenAI Chat Completions，并在模型支持时提供流式输出。',
        relayFormat: '原生 OpenAI Chat Completions 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          messages: [
            { role: 'system', content: 'You are a concise assistant.' },
            { role: 'user', content: 'Say hello in one sentence.' },
          ],
          temperature: 0.7,
          stream: false,
        }),
        notes: [
          '这是 OpenAI 兼容聊天客户端的主要端点。',
          'stream=true 时使用 Server-Sent Events，客户端应逐块读取并处理结束标记。',
          commonAvailabilityNote,
        ],
        responses: responses('聊天完成', '返回一个或多个选择和令牌用量。', {
          id: 'chatcmpl-abc123',
          object: 'chat.completion',
          created: 1710000000,
          model: 'MODEL_ID',
          choices: [
            {
              index: 0,
              message: { role: 'assistant', content: 'Hello! How can I help?' },
              finish_reason: 'stop',
            },
          ],
          usage: {
            prompt_tokens: 18,
            completion_tokens: 7,
            total_tokens: 25,
          },
        }),
      },
      {
        id: 'responses',
        category: 'chat',
        name: 'Responses',
        method: 'POST',
        path: '/v1/responses',
        description:
          '使用 OpenAI Responses 格式生成文本、工具调用或多模态响应。',
        relayFormat: '原生 OpenAI Responses 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: 'Summarize why idempotency matters in one paragraph.',
          stream: false,
        }),
        notes: [
          '适合 Codex CLI 与使用 Responses API 的新客户端。',
          '工具与多模态字段是否生效由所选模型能力决定。',
          commonAvailabilityNote,
        ],
        responses: responses('Responses 完成', '返回统一 response 对象。', {
          id: 'resp_abc123',
          object: 'response',
          status: 'completed',
          model: 'MODEL_ID',
          output: [
            {
              type: 'message',
              role: 'assistant',
              content: [
                {
                  type: 'output_text',
                  text: 'Idempotency prevents duplicate side effects.',
                },
              ],
            },
          ],
        }),
      },
      {
        id: 'responses-compact',
        category: 'chat',
        name: '压缩 Responses 上下文',
        method: 'POST',
        path: '/v1/responses/compact',
        description: '压缩较长的 Responses 会话上下文，供后续请求继续使用。',
        relayFormat: 'OpenAI Responses Compaction 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: [
            {
              role: 'user',
              content: 'Keep the important implementation decisions.',
            },
          ],
        }),
        notes: [
          '仅在对应上游与模型支持 Responses Compaction 时可用。',
          commonAvailabilityNote,
        ],
        responses: responses('上下文压缩完成', '返回可复用的压缩上下文。', {
          id: 'cmpct_abc123',
          object: 'response.compaction',
          compacted: true,
        }),
      },
      {
        id: 'claude-messages',
        category: 'chat',
        name: 'Claude Messages',
        method: 'POST',
        path: '/v1/messages',
        description: '使用 Anthropic Messages 原生协议调用 Claude 兼容模型。',
        relayFormat: '原生 Anthropic Messages 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        authHeader: 'x-api-key',
        extraHeaders: { 'anthropic-version': '2023-06-01' },
        requestBody: json({
          model: 'MODEL_ID',
          max_tokens: 256,
          messages: [
            { role: 'user', content: 'Explain retry backoff briefly.' },
          ],
        }),
        notes: [
          'Anthropic SDK 的 base URL 填站点根地址，不追加 /v1。',
          '鉴权使用 x-api-key，同时发送 anthropic-version。',
          commonAvailabilityNote,
        ],
        responses: responses(
          'Claude 消息完成',
          '返回 Anthropic message 对象。',
          {
            id: 'msg_abc123',
            type: 'message',
            role: 'assistant',
            model: 'MODEL_ID',
            content: [
              { type: 'text', text: 'Use exponential backoff with jitter.' },
            ],
            stop_reason: 'end_turn',
          }
        ),
      },
      {
        id: 'completions',
        category: 'chat',
        name: '文本补全',
        method: 'POST',
        path: '/v1/completions',
        description: '兼容旧版 OpenAI Completions 的纯文本补全端点。',
        relayFormat: 'OpenAI Completions 格式',
        responseType: 'application/json 或 text/event-stream',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          prompt: 'A reliable API client should',
          max_tokens: 64,
        }),
        notes: [
          '新项目优先使用 /v1/chat/completions 或 /v1/responses。',
          commonAvailabilityNote,
        ],
        responses: responses('补全完成', '返回文本 choices。', {
          id: 'cmpl-abc123',
          object: 'text_completion',
          model: 'MODEL_ID',
          choices: [
            {
              index: 0,
              text: ' retry transient errors with backoff.',
              finish_reason: 'stop',
            },
          ],
        }),
      },
    ],
  },
  {
    id: 'embeddings',
    title: '嵌入',
    endpoints: [
      {
        id: 'embeddings',
        category: 'embeddings',
        name: '创建嵌入',
        method: 'POST',
        path: '/v1/embeddings',
        description: '把文本转换为向量，用于检索、聚类与相似度计算。',
        relayFormat: 'OpenAI Embeddings 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          input: ['first document', 'second document'],
        }),
        notes: [commonAvailabilityNote],
        responses: responses('嵌入完成', '返回每条输入对应的向量。', {
          object: 'list',
          data: [
            {
              object: 'embedding',
              index: 0,
              embedding: [0.012, -0.031, 0.008],
            },
          ],
          model: 'MODEL_ID',
          usage: { prompt_tokens: 4, total_tokens: 4 },
        }),
      },
    ],
  },
  {
    id: 'rerank',
    title: '重排序',
    endpoints: [
      {
        id: 'rerank',
        category: 'rerank',
        name: '重排序文档',
        method: 'POST',
        path: '/v1/rerank',
        description: '按查询与候选文档的相关度重新排序。',
        relayFormat: 'Rerank 兼容格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          query: 'What is idempotency?',
          documents: ['A duplicate-safe operation.', 'A rendering technique.'],
          top_n: 2,
        }),
        notes: [commonAvailabilityNote],
        responses: responses('重排序完成', '返回按相关度排列的索引与分数。', {
          id: 'rerank-abc123',
          results: [
            { index: 0, relevance_score: 0.98 },
            { index: 1, relevance_score: 0.04 },
          ],
        }),
      },
    ],
  },
  {
    id: 'moderations',
    title: '内容审查',
    endpoints: [
      {
        id: 'moderations',
        category: 'moderations',
        name: '内容审查',
        method: 'POST',
        path: '/v1/moderations',
        description: '使用所选审查模型评估输入内容。',
        relayFormat: 'OpenAI Moderations 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({ model: 'MODEL_ID', input: 'Text to review.' }),
        notes: [commonAvailabilityNote],
        responses: responses('审查完成', '返回分类标记与分数。', {
          id: 'modr-abc123',
          model: 'MODEL_ID',
          results: [{ flagged: false, categories: {}, category_scores: {} }],
        }),
      },
    ],
  },
  {
    id: 'audio',
    title: '音频',
    endpoints: [
      {
        id: 'audio-transcriptions',
        category: 'audio',
        name: '音频转写',
        method: 'POST',
        path: '/v1/audio/transcriptions',
        description: '把音频文件转写为原始语言文本。',
        relayFormat: 'OpenAI Audio Transcriptions 格式',
        responseType: 'application/json、text 或 vtt/srt',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'file', value: 'audio.mp3', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'response_format', value: 'json' },
        ],
        notes: ['上传大小还受网关请求体限制约束。', commonAvailabilityNote],
        responses: responses('转写完成', '返回识别文本。', {
          text: 'Transcribed audio text.',
        }),
      },
      {
        id: 'audio-translations',
        category: 'audio',
        name: '音频翻译',
        method: 'POST',
        path: '/v1/audio/translations',
        description: '把支持的音频翻译并转写为英文文本。',
        relayFormat: 'OpenAI Audio Translations 格式',
        responseType: 'application/json、text 或 vtt/srt',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'file', value: 'audio.mp3', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'response_format', value: 'json' },
        ],
        notes: ['输入语言与格式支持取决于所选模型。', commonAvailabilityNote],
        responses: responses('翻译完成', '返回英文转写文本。', {
          text: 'Translated audio text.',
        }),
      },
      {
        id: 'audio-speech',
        category: 'audio',
        name: '文本转语音',
        method: 'POST',
        path: '/v1/audio/speech',
        description: '把文本合成为音频流。',
        relayFormat: 'OpenAI Audio Speech 格式',
        responseType: 'audio/mpeg 或其他请求格式',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          voice: 'VOICE_ID',
          input: 'Hello from the API.',
          response_format: 'mp3',
        }),
        notes: [
          '客户端应把响应按二进制音频保存，不要按 JSON 解析。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '语音合成完成',
          '响应体是二进制音频流。',
          '(binary audio stream)'
        ),
      },
    ],
  },
  {
    id: 'realtime',
    title: '实时',
    endpoints: [
      {
        id: 'realtime',
        category: 'realtime',
        name: 'Realtime WebSocket',
        method: 'GET',
        path: '/v1/realtime',
        description: '建立 OpenAI Realtime 兼容 WebSocket 会话。',
        relayFormat: 'OpenAI Realtime WebSocket 格式',
        responseType: 'WebSocket 事件流',
        requestKind: 'websocket',
        notes: [
          '使用 wss:// 协议并在连接请求中携带 API 令牌。',
          '会话事件与音频能力取决于所选实时模型。',
          commonAvailabilityNote,
        ],
        responses: responses(
          '连接升级成功',
          'HTTP 连接升级为 WebSocket，随后接收 session 事件。',
          json({
            type: 'session.created',
            session: { id: 'sess_abc123', model: 'MODEL_ID' },
          }),
          101
        ),
      },
    ],
  },
  {
    id: 'images',
    title: '图片',
    endpoints: [
      {
        id: 'image-generations',
        category: 'images',
        name: '生成图片',
        method: 'POST',
        path: '/v1/images/generations',
        description: '根据提示词生成图片。',
        relayFormat: 'OpenAI Images Generations 格式',
        responseType: 'application/json',
        requestKind: 'json',
        requestBody: json({
          model: 'MODEL_ID',
          prompt: 'A paper-cut illustration of a mountain API gateway.',
          n: 1,
          size: '1024x1024',
        }),
        notes: [
          '图片返回 URL 或 base64 取决于模型与请求参数。',
          commonAvailabilityNote,
        ],
        responses: responses('图片生成完成', '返回图片 URL 或 base64 数据。', {
          created: 1710000000,
          data: [{ url: 'https://example.invalid/generated-image.png' }],
        }),
      },
      {
        id: 'image-edits',
        category: 'images',
        name: '编辑图片',
        method: 'POST',
        path: '/v1/images/edits',
        description: '根据原图与提示词执行图片编辑。',
        relayFormat: 'OpenAI Images Edits 格式',
        responseType: 'application/json',
        requestKind: 'multipart',
        multipartFields: [
          { name: 'image', value: 'image.png', file: true },
          { name: 'model', value: 'MODEL_ID' },
          { name: 'prompt', value: 'Add a blue night sky.' },
        ],
        notes: [
          '可接受的图片格式与尺寸由所选模型决定。',
          commonAvailabilityNote,
        ],
        responses: responses('图片编辑完成', '返回编辑后的图片数据。', {
          created: 1710000000,
          data: [{ url: 'https://example.invalid/edited-image.png' }],
        }),
      },
    ],
  },
  {
    id: 'gemini',
    title: 'Gemini 原生',
    endpoints: [
      {
        id: 'gemini-generate-content',
        category: 'gemini',
        name: 'GenerateContent',
        method: 'POST',
        path: '/v1beta/models/{model}:generateContent',
        description: '使用 Gemini 原生 generateContent 协议生成内容。',
        relayFormat: '原生 Gemini generateContent 格式',
        responseType: 'application/json',
        requestKind: 'json',
        authHeader: 'x-goog-api-key',
        requestBody: json({
          contents: [
            {
              role: 'user',
              parts: [{ text: 'Explain circuit breakers in one sentence.' }],
            },
          ],
        }),
        notes: [
          '把 {model} 替换为真实 Gemini model id。',
          '鉴权使用 x-goog-api-key。',
          commonAvailabilityNote,
        ],
        responses: responses('内容生成完成', '返回 Gemini candidates。', {
          candidates: [
            {
              content: {
                role: 'model',
                parts: [
                  {
                    text: 'A circuit breaker stops repeated calls to a failing dependency.',
                  },
                ],
              },
              finishReason: 'STOP',
            },
          ],
        }),
      },
    ],
  },
]

export const allEndpoints: readonly EndpointDoc[] = endpointCategories.flatMap(
  (category) => category.endpoints
)

export const defaultEndpointId = 'chat-completions'

const endpointMap = new Map<string, EndpointDoc>(
  allEndpoints.map((endpoint) => [endpoint.id, endpoint])
)

export function getEndpoint(id?: string): EndpointDoc {
  const selected = endpointMap.get(id ?? '')
  if (selected) return selected

  const fallback = endpointMap.get(defaultEndpointId)
  if (fallback) return fallback

  throw new Error(`Default endpoint ${defaultEndpointId} is not registered`)
}
