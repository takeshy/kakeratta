#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

PROJECT_ID=${KAKERATTA_PROJECT_ID:-kakeratta}
REGION=${KAKERATTA_REGION:-asia-northeast1}
ZONE=${KAKERATTA_ZONE:-asia-northeast1-b}
IMAGE_TAG=${KAKERATTA_IMAGE_TAG:-$(date -u +%Y%m%d%H%M%S)}
TF_ARGS=(-var="project_id=$PROJECT_ID" -var="region=$REGION" -var="zone=$ZONE" -var="image_tag=$IMAGE_TAG")

terraform init

# The registry and APIs must exist before Cloud Build can publish the image.
terraform apply -auto-approve \
  -target='google_project_service.required' \
  -target='google_artifact_registry_repository.app' \
  -target='google_project_iam_member.cloud_build' \
  "${TF_ARGS[@]}"

IMAGE="$REGION-docker.pkg.dev/$PROJECT_ID/kakeratta/kakeratta:$IMAGE_TAG"
gcloud builds submit .. --project="$PROJECT_ID" --config=../cloudbuild.yaml \
  --substitutions="_IMAGE=$IMAGE,_VERSION=$IMAGE_TAG"

terraform apply -auto-approve "${TF_ARGS[@]}"

URL=$(terraform output -raw url)
INSTANCE=$(terraform output -raw instance)
gcloud compute instances reset "$INSTANCE" --project="$PROJECT_ID" --zone="$ZONE" --quiet

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
printf 'login URL: run `cd infra && terraform output -raw login_url`\n'
