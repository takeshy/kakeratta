import { loginView } from "../src/login.js";
import { t } from "../src/i18n.js";

export default { title: "Login", parameters: { layout: "fullscreen" } };
export const Ready = { name: "Googleでログイン", render: () => loginView({ onLogin: () => {} }) };
export const Checking = { name: "ログイン状態を確認中", render: () => loginView({ state: "checking" }) };
export const Authorizing = { name: "Googleの認証待ち", render: () => loginView({ state: "authorizing" }) };
export const Completing = { name: "認証後・ログインを完了中", render: () => loginView({ state: "completing" }) };
export const Loading = { name: "アプリを読み込み中", render: () => loginView({ state: "loading" }) };
export const Failed = { name: "失敗した場合のみ再ログイン", render: () => loginView({ onLogin: () => {}, error: t("ログインセッションを開始できませんでした。") }) };
