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
import { describe, expect, it } from 'vitest'

import type { PricingModel } from '../../types'
import { getModelCategories, filterByCategory } from '../model-categories'

const model = (values: Partial<PricingModel> = {}): PricingModel => ({
  id: 1,
  model_name: 'opaque-model-id',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
  ...values,
})

describe('model capability categories', () => {
  it('keeps missing metadata and default OpenAI protocol in all only without guessing names', () => {
    const unknown = model({
      model_name: 'gpt-image-video-embedding',
      supported_endpoint_types: ['openai'],
    })
    expect(getModelCategories(unknown)).toEqual([])
    expect(filterByCategory([unknown], 'all')).toEqual([unknown])
    expect(filterByCategory([unknown], 'text')).toEqual([])
  })
  it('does not confuse vision input with generated images', () => {
    expect(
      getModelCategories(
        model({
          input_modalities: ['text', 'image'],
          output_modalities: ['text'],
          capabilities: ['vision'],
        })
      )
    ).toEqual(['text'])
  })
  it('keeps pure audio transcription out of text despite text output and a default chat protocol', () => {
    expect(
      getModelCategories(
        model({
          input_modalities: ['audio'],
          output_modalities: ['text'],
          supported_endpoint_types: ['openai'],
        })
      )
    ).toEqual(['audio'])
  })
  it('includes multimodal output in every matching category without duplicates', () => {
    expect(
      getModelCategories(
        model({
          output_modalities: ['text', 'image', 'video', 'audio'],
          supported_endpoint_types: ['image-generation', 'openai-video'],
        })
      )
    ).toEqual(['text', 'images', 'video', 'audio'])
  })
  it('groups embedding and rerank protocols under vectors', () => {
    expect(
      getModelCategories(
        model({
          supported_endpoint_types: ['embeddings', 'jina-rerank'],
          capabilities: ['embeddings'],
        })
      )
    ).toEqual(['vectors'])
  })
  it('uses explicit capabilities when modalities and endpoints are absent', () => {
    expect(
      getModelCategories(
        model({ capabilities: ['image_generation', 'speech_recognition'] })
      )
    ).toEqual(['images', 'audio'])
  })
})
