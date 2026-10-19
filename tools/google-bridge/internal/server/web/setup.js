import { el, clear } from "./dom.js";

const projectPattern = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/;
const clientPattern = /^[A-Za-z0-9_-]+\.apps\.googleusercontent\.com$/;
const MAX_CLIENT_BYTES = 64 * 1024;

// Every interpolated shell argument has a strict alphabet; never execute the
// generated plan in the server or silently change gcloud's default project.
export function setupPlan({ projectId, services, createProject = false }) {
  if (!projectPattern.test(projectId) || !Array.isArray(services) || !services.length || services.some(s => !["gmail", "calendar"].includes(s))) {
    throw new Error("プロジェクトID（6〜30文字の英小文字・数字・ハイフン）と利用サービスを確認してください。");
  }
  const apis = [...new Set(services)].map(s => s === "gmail" ? "gmail.googleapis.com" : "calendar-json.googleapis.com");
  const project = `--project=${projectId}`;
  return {
    commands: [
      "# 本人の端末で実行。最初に操作アカウントを確認してください。",
      "gcloud auth list --filter=status:ACTIVE --format='value(account)'",
      ...(createProject ? [`gcloud projects create ${projectId}`] : []),
      `gcloud projects describe ${projectId} --format='value(projectId,lifecycleState)'`,
      `gcloud services enable ${apis.join(" ")} ${project}`,
      `gcloud services list --enabled ${project} --format='value(config.name)'`,
    ].join("\n"),
    apis,
    links: {
      project: "https://console.cloud.google.com/projectcreate",
      apis: apis.map(api => `https://console.cloud.google.com/apis/library/${api}?project=${projectId}`),
      branding: `https://console.cloud.google.com/auth/branding?project=${projectId}`,
      audience: `https://console.cloud.google.com/auth/audience?project=${projectId}`,
      scopes: `https://console.cloud.google.com/auth/scopes?project=${projectId}`,
      clients: `https://console.cloud.google.com/auth/clients?project=${projectId}`,
    },
  };
}

export function parseOAuthClient(text, redirectUri) {
  if (typeof text !== "string" || new TextEncoder().encode(text).length > MAX_CLIENT_BYTES) throw new Error("OAuth設定JSONは64KiB以下にしてください。");
  let value;
  try { value = JSON.parse(text); } catch { throw new Error("GoogleからダウンロードしたOAuth設定JSONを選択してください。"); }
  const web = value?.web;
  if (!web || value.installed || !projectPattern.test(web.project_id || "") || typeof web.client_id !== "string" || web.client_id.length > 256 || !clientPattern.test(web.client_id) || typeof web.client_secret !== "string" || web.client_secret.length < 8 || web.client_secret.length > 1024 || /[\r\n\0]/.test(web.client_secret)) {
    throw new Error("Web application用のOAuth設定JSONが必要です。Google Auth PlatformのClientsから取得してください。");
  }
  if (!Array.isArray(web.redirect_uris) || !web.redirect_uris.includes(redirectUri)) {
    throw new Error("JSONのリダイレクトURIがこのアプリと一致しません。Google側に表示中のURIを登録し、JSONを再ダウンロードしてください。");
  }
  // Endpoint URLs from the file are intentionally ignored. The server pins all
  // OAuth endpoints to Google and independently validates submitted fields.
  return { projectId: web.project_id, clientId: web.client_id, clientSecret: web.client_secret };
}

export async function readOAuthClient(file, redirectUri) {
  if (!file || file.size > MAX_CLIENT_BYTES) throw new Error("OAuth設定JSONは64KiB以下にしてください。");
  return parseOAuthClient(await file.text(), redirectUri);
}

const link = (label, href) => el("a", { text: label, href, target: "_blank", rel: "noopener noreferrer" });
const field = (label, input) => el("label", { class: "field" }, el("span", { text: label }), input);

export function createSetupGuide(redirectUri, onUseProject) {
  const details = el("details", { class: "setup-guide" }, el("summary", { text: "Google Cloudのセットアップを案内する" }));
  const project = el("input", { type: "text", autocomplete: "off", spellcheck: "false", placeholder: "my-google-bridge", maxlength: 30 });
  const gmail = el("input", { type: "checkbox" }); gmail.checked = true;
  const calendar = el("input", { type: "checkbox" });
  const create = el("input", { type: "checkbox" });
  const error = el("p", { role: "alert" });
  const steps = el("div", { "aria-live": "polite" });
  const check = (input, label) => el("label", {}, input, el("span", { text: label }));
  const form = el("form", { onsubmit: event => {
    event.preventDefault(); error.textContent = ""; clear(steps);
    try {
      const id = project.value.trim();
      const plan = setupPlan({ projectId: id, services: [gmail.checked && "gmail", calendar.checked && "calendar"].filter(Boolean), createProject: create.checked });
      onUseProject(id);
      steps.append(
        el("h3", { text: "1. プロジェクトとAPIを準備する" }),
        el("p", { class: "hint", text: "以下は本人の端末で実行する手順です。この画面はGoogle Cloudを変更せず、実行済みとも判定しません。gcloud未設定ならgcloud initでログインするか、Consoleで同じ設定を行ってください。権限・組織の制約で失敗した場合もConsoleで状態を確認できます。" }),
        ...(create.checked ? [link("Consoleでプロジェクトを作成", plan.links.project)] : []),
        el("pre", { text: plan.commands }),
        el("p", { text: `有効なAPI一覧に次があることを確認してください: ${plan.apis.join(", ")}` }),
        ...plan.links.apis.map((href, i) => link(`${plan.apis[i]}をConsoleで確認`, href)),
        el("h3", { text: "2. 同意画面と公開状態を設定する" }),
        el("p", { text: "Brandingにアプリ情報を設定し、Audienceで利用形態を選びます。External / Testingなら本人をテストユーザーへ追加し、Data Accessには下の接続フォームで利用する権限だけを設定してください。" }),
        link("Branding", plan.links.branding), link("Audience・テストユーザー", plan.links.audience), link("Data Access", plan.links.scopes),
        el("p", { class: "hint", text: "Gmail本文のreadonly / composeはRestricted、sendはSensitiveです。Testingでは通常7日でrefresh tokenが失効します。Publishing statusと個人利用例外を確認してください。ユーザー所有プロジェクトでも審査・セキュリティ評価の免除を保証しません。" }),
        link("Googleの審査要件を確認", "https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification"),
        el("h3", { text: "3. Web用OAuthクライアントを作る" }),
        el("p", { text: "ClientsでWeb applicationを選び、Authorized redirect URIsに次のURIを完全一致で登録します。ローカル起動でもDesktop appではなくWeb applicationを使います。" }),
        el("code", { text: redirectUri }), link("Clientsを開く", plan.links.clients),
        el("p", { text: "作成したJSONをダウンロードし、下のGmailまたはCalendarの接続フォームで選択してください。手入力でも接続できます。ログイン・同意・本人確認はGoogleの画面で本人が行います。" }),
        el("h3", { text: "4. 接続して状態を確認する" }),
        el("p", { text: "下の接続フォームで必要な権限とデータ転送への同意を選び、Googleに接続します。接続後の状態・許可済み権限・最終接続確認を確認してください。途中で失敗しても、同じプロジェクトを指定して該当手順から再開できます。" }),
      );
    } catch (e) { error.textContent = e.message; }
  } });
  form.append(field("セットアップするプロジェクトID", project), check(create, "新規プロジェクトを作成する手順も表示する"), check(gmail, "Gmailを使う"), check(calendar, "Google Calendarを使う"), el("button", { type: "submit", text: "この設定の手順を表示" }), error);
  details.append(form, steps);
  return details;
}

export function recoveryHint(message) {
  if (message === "redirect_uri_mismatch") return "GoogleのClientsで、この画面のリダイレクトURIをAuthorized redirect URIsへ完全一致で登録してください。変更したJSONは再ダウンロードします。";
  if (message === "access_denied") return "Googleで接続を中止したか、テストユーザー・組織の制限があります。Audienceと利用アカウントを確認して再接続してください。";
  if (message === "invalid_client" || message === "Invalid Google connection input") return "同じプロジェクトのWeb application用client ID・secretを確認してください。JSONから読み込み直すか、3項目を入力し直して再接続してください。";
  if (/permission is missing/.test(message)) return "下のフォームで必要な権限を選び直して再接続してください。Googleの同意画面でも対象の権限を許可します。";
  if (/expired|revoked|another browser/.test(message)) return "元のブラウザーの同じタブから再接続してください。保存済みクライアントを使う場合は3項目を空欄にします。Testingの有効期限やGoogle側の許可取消も確認してください。";
  if (/activation|account permissions/.test(message)) return "対象プロジェクトのAPI有効化とGoogleアカウントの権限を確認し、接続テストを再実行してください。上のセットアップ案内からConsoleへ進めます。";
  if (/temporarily|rate limited/.test(message)) return "時間を置いて再試行してください。Google CloudのAPI割り当て・障害情報も確認できます。送信や予定変更の結果が不明な操作は自動再送しません。";
  if (/same Google account/.test(message)) return "GmailとCalendarを同じGoogleアカウントで接続してください。別アカウントへ変更する場合は既存の両接続を解除します。";
  return "上のセットアップ案内で対象API・テストユーザー・Web用クライアント・リダイレクトURIを確認し、同じタブから接続をやり直してください。秘密情報をチャットへ貼り付ける必要はありません。";
}
