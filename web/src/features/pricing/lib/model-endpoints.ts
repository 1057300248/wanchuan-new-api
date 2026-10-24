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
import type { PricingModel } from '../types'
import { getModelCategories } from './model-categories'

export type ModelEndpointMap = Record<
  string,
  { path?: string; method?: string }
>

/** Explicit protocol links, never a guessed chat fallback for an unknown protocol. */
export function getEndpointDocId(type: string): string | undefined {
  const docs: Record<string, string> = {
    openai: 'chat-completions',
    'openai-response': 'responses',
    'openai-response-compact': 'responses-compact',
    anthropic: 'claude-messages',
    gemini: 'gemini-generate-content',
    embeddings: 'embeddings',
    'jina-rerank': 'rerank',
    'image-generation': 'image-generations',
    'openai-video': 'videos-create',
    'audio-speech': 'audio-speech',
    'audio-transcription': 'audio-transcriptions',
    'audio-translation': 'audio-translations',
    realtime: 'realtime',
  }
  return docs[type]
}

export function getModelPlayground(
  model: PricingModel
): '/image' | '/playground' | undefined {
  const endpoints = model.supported_endpoint_types ?? []
  const categories = getModelCategories(model)
  if (endpoints.includes('image-generation') && categories.includes('images')) {
    return '/image'
  }
  if (endpoints.includes('openai') && categories.includes('text')) {
    return '/playground'
  }
  return undefined
}
