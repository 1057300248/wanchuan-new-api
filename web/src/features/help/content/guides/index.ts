import type { GuideDoc } from '../types'

import { anthropicSdk } from './anthropic-sdk'
import { authentication } from './authentication'
import { billing } from './billing'
import { ccSwitch } from './cc-switch'
import { cherryStudio } from './cherry-studio'
import { claudeCode } from './claude-code'
import { clineRoo } from './cline-roo'
import { codexCli } from './codex-cli'
import { compatibility } from './compatibility'
import { consoleTour } from './console-tour'
import { functionCalling } from './function-calling'
import { immersiveTranslate } from './immersive-translate'
import { openaiSdk } from './openai-sdk'
import { playground } from './playground'
import { quickStart } from './quick-start'
import { streaming } from './streaming'
import { troubleshooting } from './troubleshooting'
import { vision } from './vision'

export const guides: readonly GuideDoc[] = [
  quickStart,
  authentication,
  consoleTour,
  billing,
  playground,
  claudeCode,
  codexCli,
  ccSwitch,
  cherryStudio,
  clineRoo,
  immersiveTranslate,
  openaiSdk,
  anthropicSdk,
  compatibility,
  streaming,
  functionCalling,
  vision,
  troubleshooting,
]
