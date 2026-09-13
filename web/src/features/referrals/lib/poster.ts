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
import { CHUANAPI_BRAND_LOGO } from '@/extensions/chuanapi/brand'

export const REFERRAL_POSTER_WIDTH = 720
export const REFERRAL_POSTER_HEIGHT = 1080
export const REFERRAL_POSTER_FILE_NAME = 'referral-poster.png'

export interface ReferralPosterOptions {
  /** Affiliate link rendered next to the QR code. */
  link: string
  /** Pre-rendered QR canvas (the only dynamic image on the template). */
  qrCanvas: HTMLCanvasElement
  /** Fixed template copy, already translated by the caller. */
  headline: string
  rewardLine: string
  cta: string
}

function loadTemplateImage(src: string): Promise<HTMLImageElement | null> {
  return new Promise((resolve) => {
    const image = new Image()
    image.addEventListener('load', () => resolve(image), { once: true })
    image.addEventListener('error', () => resolve(null), { once: true })
    image.src = src
  })
}

function wrapText(
  ctx: CanvasRenderingContext2D,
  text: string,
  maxWidth: number
): string[] {
  const lines: string[] = []
  for (const paragraph of text.split('\n')) {
    let line = ''
    for (const word of paragraph.split(/\s+/)) {
      const candidate = line ? `${line} ${word}` : word
      if (line && ctx.measureText(candidate).width > maxWidth) {
        lines.push(line)
        line = word
      } else {
        line = candidate
      }
    }
    lines.push(line)
  }
  return lines
}

function drawCenteredLines(
  ctx: CanvasRenderingContext2D,
  lines: string[],
  centerX: number,
  startY: number,
  lineHeight: number
): number {
  let y = startY
  for (const line of lines) {
    ctx.fillText(line, centerX, y)
    y += lineHeight
  }
  return y
}

function drawTemplate(
  ctx: CanvasRenderingContext2D,
  options: ReferralPosterOptions,
  logo: HTMLImageElement | null
) {
  const { width, height } = ctx.canvas
  const background = ctx.createLinearGradient(0, 0, 0, height)
  background.addColorStop(0, '#0f172a')
  background.addColorStop(1, '#1e293b')
  ctx.fillStyle = background
  ctx.fillRect(0, 0, width, height)

  if (logo) {
    const logoSize = 88
    ctx.drawImage(logo, (width - logoSize) / 2, 64, logoSize, logoSize)
  }

  ctx.textAlign = 'center'
  ctx.fillStyle = '#f8fafc'
  ctx.font = '700 44px sans-serif'
  let y = drawCenteredLines(
    ctx,
    wrapText(ctx, options.headline, width - 120),
    width / 2,
    208,
    58
  )

  ctx.fillStyle = '#93c5fd'
  ctx.font = '500 28px sans-serif'
  y = drawCenteredLines(
    ctx,
    wrapText(ctx, options.rewardLine, width - 120),
    width / 2,
    y + 12,
    40
  )

  const qrSize = 360
  const qrTop = y + 36
  const cardPadding = 24
  const cardLeft = (width - qrSize - cardPadding * 2) / 2
  ctx.fillStyle = '#ffffff'
  ctx.beginPath()
  ctx.roundRect(
    cardLeft,
    qrTop,
    qrSize + cardPadding * 2,
    qrSize + cardPadding * 2,
    24
  )
  ctx.fill()
  ctx.drawImage(
    options.qrCanvas,
    width / 2 - qrSize / 2,
    qrTop + cardPadding,
    qrSize,
    qrSize
  )

  ctx.fillStyle = '#e2e8f0'
  ctx.font = '400 22px monospace'
  drawCenteredLines(
    ctx,
    wrapText(ctx, options.link, width - 120).slice(0, 3),
    width / 2,
    qrTop + qrSize + cardPadding * 2 + 60,
    32
  )

  ctx.fillStyle = '#f8fafc'
  ctx.font = '600 30px sans-serif'
  drawCenteredLines(ctx, [options.cta], width / 2, height - 96, 40)
}

function canvasToBlob(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) {
        resolve(blob)
      } else {
        reject(new Error('referral poster export produced an empty image'))
      }
    }, 'image/png')
  })
}

/**
 * Renders the fixed referral poster template with only the QR code and the
 * affiliate link changing, then downloads it as a real PNG file.
 */
export async function downloadReferralPoster(
  options: ReferralPosterOptions
): Promise<void> {
  if (!options.link || !options.qrCanvas) {
    throw new Error('referral poster requires a link and a QR code')
  }
  const canvas = document.createElement('canvas')
  canvas.width = REFERRAL_POSTER_WIDTH
  canvas.height = REFERRAL_POSTER_HEIGHT
  const ctx = canvas.getContext('2d')
  if (!ctx) {
    throw new Error('referral poster requires a 2D canvas context')
  }
  const logo = await loadTemplateImage(CHUANAPI_BRAND_LOGO)
  drawTemplate(ctx, options, logo)

  const blob = await canvasToBlob(canvas)
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = REFERRAL_POSTER_FILE_NAME
  anchor.rel = 'noopener'
  anchor.click()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
