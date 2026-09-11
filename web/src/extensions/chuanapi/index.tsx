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
import { lazy, Suspense, type ReactNode } from "react";

import type { BrandExtension } from "../brand";
import type {
  HelpApiReferencePage,
  HelpExtension,
  HelpGuidePage,
  HelpIndexPage,
} from "../help";
import type { I18nExtension } from "../i18n";
import type { PaymentSettingsExtension } from "../payment-settings";
import type { WalletExtension, WalletPage } from "../wallet";
import { CHUANAPI_BRAND_LOGO } from "./brand";
import { loadChuanapiI18nOverride } from "./i18n";

export {
  CHUANAPI_PUBLIC_HOME,
  chuanapiNavigationExtension,
} from "./navigation";
export { SHOW_LEGACY_AFFILIATE_QUOTA } from "./flags";
export { CHUANAPI_BRAND_LOGO } from "./brand";
export { loadChuanapiI18nOverride } from "./i18n";

const LazyDocsIndexPage = lazy(() =>
  import("./help/pages/docs-index").then(({ DocsIndexPage }) => ({
    default: DocsIndexPage,
  })),
);
const LazyGuidePage = lazy(() =>
  import("./help/pages/guide-page").then(({ GuidePage }) => ({
    default: GuidePage,
  })),
);
const LazyApiReferencePage = lazy(() =>
  import("./help/pages/api-reference-page").then(({ ApiReferencePage }) => ({
    default: ApiReferencePage,
  })),
);
const LazyWalletPage = lazy(() =>
  import("./wallet").then(({ Wallet }) => ({ default: Wallet })),
);
const LazyPaymentSettingsPage = lazy(() =>
  import("./payment").then(({ chuanapiPaymentSettingsExtension }) => ({
    default: ({ settings }: { settings: unknown }) => (
      <>{chuanapiPaymentSettingsExtension.render(settings)}</>
    ),
  })),
);

function ExtensionLoading() {
  return <div className="min-h-24" role="status" aria-label="Loading" />;
}

function withLoading(content: ReactNode) {
  return <Suspense fallback={<ExtensionLoading />}>{content}</Suspense>;
}

const LazyHelpIndex: HelpIndexPage = () => withLoading(<LazyDocsIndexPage />);
const LazyHelpGuide: HelpGuidePage = ({ slug }) =>
  withLoading(<LazyGuidePage slug={slug} />);
const LazyHelpApiReference: HelpApiReferencePage = (props) =>
  withLoading(<LazyApiReferencePage {...props} />);
const LazyWallet: WalletPage = (props) =>
  withLoading(<LazyWalletPage {...props} />);

export const chuanapiBrandExtension: BrandExtension = {
  id: "chuanapi/brand",
  logoUrl: CHUANAPI_BRAND_LOGO,
};

export const chuanapiHelpExtension: HelpExtension = {
  id: "chuanapi/help",
  indexPage: LazyHelpIndex,
  guidePage: LazyHelpGuide,
  apiReferencePage: LazyHelpApiReference,
};

export const chuanapiI18nExtension: I18nExtension = {
  id: "chuanapi/i18n",
  loadOverride: loadChuanapiI18nOverride,
};

export const chuanapiWalletExtension: WalletExtension = {
  id: "chuanapi/wallet",
  page: LazyWallet,
};

export const chuanapiPaymentSettingsExtension: PaymentSettingsExtension = {
  id: "chuanapi/payment-settings",
  render: (settings) =>
    withLoading(<LazyPaymentSettingsPage settings={settings} />),
};
