#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

PROJECT_ID=${KAKERATTA_PROJECT_ID:-kakeratta}
REGION=${KAKERATTA_REGION:-asia-northeast1}
ZONE=${KAKERATTA_ZONE:-asia-northeast1-b}
RUN_REGION=${KAKERATTA_RUN_REGION:-asia-east1}
IMAGE_TAG=${KAKERATTA_IMAGE_TAG:-$(date -u +%Y%m%d%H%M%S)}
COMMIT_ID=$(git rev-parse HEAD)
FIREBASE_PROJECT_ID=${KAKERATTA_FIREBASE_PROJECT_ID:-$PROJECT_ID}

# Firebase's browser configuration is public metadata. Fetch the active Web
# App configuration from Firebase instead of maintaining a local config file.
FIREBASE_ACCESS_TOKEN=$(gcloud auth print-access-token)
FIREBASE_APPS=$(curl -fsS \
  -H "Authorization: Bearer $FIREBASE_ACCESS_TOKEN" \
  -H "x-goog-user-project: $FIREBASE_PROJECT_ID" \
  "https://firebase.googleapis.com/v1beta1/projects/$FIREBASE_PROJECT_ID/webApps")
FIREBASE_APP_ID=$(jq -r '.apps[] | select(.state == "ACTIVE") | .appId' <<<"$FIREBASE_APPS" | head -n 1)
if [[ -z "$FIREBASE_APP_ID" ]]; then
  printf 'No active Firebase Web App exists in project %s.\n' "$FIREBASE_PROJECT_ID" >&2
  exit 1
fi
FIREBASE_CONFIG=$(curl -fsS \
  -H "Authorization: Bearer $FIREBASE_ACCESS_TOKEN" \
  -H "x-goog-user-project: $FIREBASE_PROJECT_ID" \
  "https://firebase.googleapis.com/v1beta1/projects/$FIREBASE_PROJECT_ID/webApps/$FIREBASE_APP_ID/config")
FIREBASE_API_KEY=$(jq -er '.apiKey' <<<"$FIREBASE_CONFIG")
FIREBASE_AUTH_DOMAIN=$(jq -er '.authDomain' <<<"$FIREBASE_CONFIG")
# Judge registration uses Firebase email/password only behind the secret invite
# URL. API sessions still require the server-issued judge custom claim.
curl -fsS -X PATCH \
  -H "Authorization: Bearer $FIREBASE_ACCESS_TOKEN" \
  -H "x-goog-user-project: $FIREBASE_PROJECT_ID" \
  -H "Content-Type: application/json" \
  "https://identitytoolkit.googleapis.com/admin/v2/projects/$FIREBASE_PROJECT_ID/config?updateMask=signIn.email.enabled,signIn.email.passwordRequired" \
  -d '{"signIn":{"email":{"enabled":true,"passwordRequired":true}}}' >/dev/null
TF_ARGS=(
  -var="project_id=$PROJECT_ID"
  -var="region=$REGION"
  -var="zone=$ZONE"
  -var="run_region=$RUN_REGION"
  -var="image_tag=$IMAGE_TAG"
  -var="firebase_project_id=$FIREBASE_PROJECT_ID"
  -var="firebase_api_key=$FIREBASE_API_KEY"
  -var="firebase_auth_domain=$FIREBASE_AUTH_DOMAIN"
)
JUDGE_KEY_FILE=${KAKERATTA_JUDGE_KEY_FILE:-../tmp/judge_token_factory.key}
if [[ -s "$JUDGE_KEY_FILE" ]]; then
  JUDGE_TOKEN_FACTORY_KEY=$(<"$JUDGE_KEY_FILE")
  TF_ARGS+=(-var="judge_token_factory_key=$JUDGE_TOKEN_FACTORY_KEY")
fi
# Optional here: a terraform.tfvars in infra/ works as well, and is the better
# place for the MCP server list.
if [[ -n "${KAKERATTA_MCP_SERVERS:-}" ]]; then
  TF_ARGS+=(-var="mcp_servers=$KAKERATTA_MCP_SERVERS")
fi

terraform init

# The registry and APIs must exist before Cloud Build can publish the image.
terraform apply -auto-approve \
  -target='google_project_service.required' \
  -target='google_artifact_registry_repository.app' \
  -target='google_project_iam_member.cloud_build' \
  "${TF_ARGS[@]}"

IMAGE="$REGION-docker.pkg.dev/$PROJECT_ID/kakeratta/kakeratta:$IMAGE_TAG"
gcloud builds submit .. --project="$PROJECT_ID" --config=../cloudbuild.yaml \
  --substitutions="_IMAGE=$IMAGE,_VERSION=$COMMIT_ID"

terraform apply -auto-approve "${TF_ARGS[@]}"

cd ..
node frontend/build.mjs
npx --yes firebase-tools deploy --only hosting --project="$FIREBASE_PROJECT_ID"
URL="https://$FIREBASE_PROJECT_ID.web.app"

for attempt in $(seq 1 30); do
  if curl -fsS --max-time 5 "$URL/" >/dev/null; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    printf 'Deployment did not become ready: %s\n' "$URL" >&2
    exit 1
  fi
  sleep 5
done

printf '\nkakeratta: %s\n' "$URL"
printf 'Commit: %s\n' "$COMMIT_ID"
printf 'Cloud Run: '
terraform -chdir=infra output -raw cloud_run_url
printf '\n'
printf 'Judge URL: '
terraform -chdir=infra output -raw judge_url
printf '\n'
