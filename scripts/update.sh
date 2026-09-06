#!/usr/bin/env bash
# Backup and upgrade an existing MathStudyPlatform deployment.

set -Eeuo pipefail
umask 077

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

VERSION=""
COMPOSE_FILE="docker-compose.yml"
ENV_FILE=".env"
BACKUP_ROOT="${BACKUP_ROOT:-backups}"
UPLOADS_DIR="${MSP_UPLOADS_BACKUP_DIR:-uploads}"
BACKEND_IMAGE="ghcr.io/fraternity-z/mathstudyplatform-backend"
FRONTEND_IMAGE="ghcr.io/fraternity-z/mathstudyplatform-frontend"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" > /dev/null 2>&1 && pwd)"
PROJECT_ROOT="$(cd -- "${SCRIPT_DIR}/.." > /dev/null 2>&1 && pwd)"

usage() {
    cat <<'EOF'
用法: sudo bash ./scripts/update.sh --version <标签>

标签接受 latest、v1.0.0 或 sha-abcdef1。私有 GHCR 镜像可通过
GHCR_USERNAME 和 GHCR_TOKEN 环境变量登录。
此脚本仅支持已有完整部署；仓库不提供首次生产部署脚本。
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] || { echo "--version 缺少参数" >&2; exit 2; }
            VERSION="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "未知参数: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if ! [[ "$VERSION" =~ ^(latest|v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)|sha-[0-9a-f]{7,40})$ ]]; then
    echo -e "${RED}必须使用 --version 指定 latest、v1.0.0 或 sha-abcdef1${NC}" >&2
    exit 1
fi
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}请使用 root 权限运行此脚本${NC}" >&2
    exit 1
fi

cd "$PROJECT_ROOT"

compose() {
    "${DOCKER_COMPOSE[@]}" -f "$COMPOSE_FILE" "$@"
}

wait_for_postgres() {
    local max_attempts="${1:-30}" attempt
    for ((attempt = 1; attempt <= max_attempts; attempt++)); do
        if compose exec -T postgres sh -ec 'pg_isready -q -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}"' > /dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    compose logs --tail=50 postgres >&2 || true
    return 1
}

service_container_id() {
    compose ps -a -q "$1" 2>/dev/null || true
}

service_is_running() {
    local container_id
    container_id="$(service_container_id "$1")"
    [ -n "$container_id" ] || return 1
    [ "$(docker inspect --format '{{.State.Running}}' "$container_id" 2>/dev/null || true)" = "true" ]
}

service_image_value() {
    local service="$1" field="$2" container_id
    container_id="$(service_container_id "$service")"
    docker inspect --format "$field" "$container_id" 2>/dev/null || printf '%s\n' "unknown"
}

backup_postgres() {
    local output_path="$1"
    if ! compose exec -T postgres sh -ec 'exec pg_dump --format=custom --no-owner --no-privileges -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}"' > "$output_path"; then
        rm -f -- "$output_path"
        return 1
    fi
    [ -s "$output_path" ] || { rm -f -- "$output_path"; return 1; }
}

restart_previous_apps() {
    local services=()
    [ "$BACKEND_WAS_RUNNING" = true ] && services+=(backend)
    [ "$FRONTEND_WAS_RUNNING" = true ] && services+=(frontend)
    [ "$VECTOR_WORKER_WAS_RUNNING" = true ] && services+=(vector-worker)
    [ "${#services[@]}" -eq 0 ] || compose start "${services[@]}" || true
}

persist_image_version() {
    if grep -q '^IMAGE_VERSION=' "$ENV_FILE"; then
        sed -i "s/^IMAGE_VERSION=.*/IMAGE_VERSION=${VERSION}/" "$ENV_FILE"
    else
        printf '\nIMAGE_VERSION=%s\n' "$VERSION" >> "$ENV_FILE"
    fi
}

echo -e "${GREEN}=== MathStudyPlatform 更新 ===${NC}"
echo -e "${YELLOW}目标版本: ${VERSION}${NC}"

command -v docker > /dev/null 2>&1 || { echo -e "${RED}Docker 未安装${NC}" >&2; exit 1; }
if docker compose version > /dev/null 2>&1; then
    DOCKER_COMPOSE=(docker compose)
elif command -v docker-compose > /dev/null 2>&1; then
    DOCKER_COMPOSE=(docker-compose)
else
    echo -e "${RED}Docker Compose 未安装${NC}" >&2
    exit 1
fi
[ -f "$COMPOSE_FILE" ] || { echo -e "${RED}找不到 ${COMPOSE_FILE}${NC}" >&2; exit 1; }
[ -f "$ENV_FILE" ] || { echo -e "${RED}找不到 ${ENV_FILE}；此脚本仅支持已有部署，请先按部署指南完成初始安装${NC}" >&2; exit 1; }
if [ -z "$(service_container_id backend)" ] || [ -z "$(service_container_id frontend)" ]; then
    echo -e "${RED}未检测到完整的现有部署；此脚本不支持首次部署${NC}" >&2
    exit 1
fi
if [ -n "${GHCR_TOKEN:-}" ]; then
    [ -n "${GHCR_USERNAME:-}" ] || { echo -e "${RED}设置 GHCR_TOKEN 时必须同时设置 GHCR_USERNAME${NC}" >&2; exit 1; }
    printf '%s' "$GHCR_TOKEN" | docker login ghcr.io --username "$GHCR_USERNAME" --password-stdin
fi

BACKEND_WAS_RUNNING=false
FRONTEND_WAS_RUNNING=false
VECTOR_WORKER_WAS_RUNNING=false
service_is_running backend && BACKEND_WAS_RUNNING=true
service_is_running frontend && FRONTEND_WAS_RUNNING=true
service_is_running vector-worker && VECTOR_WORKER_WAS_RUNNING=true
BACKUP_DIR="${BACKUP_ROOT}/$(date +%Y%m%d_%H%M%S)"
mkdir -p "$BACKUP_ROOT"
mkdir "$BACKUP_DIR"
cp -- "$ENV_FILE" "$BACKUP_DIR/.env"
compose config > "$BACKUP_DIR/docker-compose.resolved.yml"
{
    printf 'backend=%s\n' "$(service_image_value backend '{{.Config.Image}}')"
    printf 'backend_image_id=%s\n' "$(service_image_value backend '{{.Image}}')"
    printf 'frontend=%s\n' "$(service_image_value frontend '{{.Config.Image}}')"
    printf 'frontend_image_id=%s\n' "$(service_image_value frontend '{{.Image}}')"
    printf 'backend_was_running=%s\n' "$BACKEND_WAS_RUNNING"
    printf 'frontend_was_running=%s\n' "$FRONTEND_WAS_RUNNING"
    printf 'vector_worker_was_running=%s\n' "$VECTOR_WORKER_WAS_RUNNING"
    if [ -n "$(service_container_id vector-worker)" ]; then
        printf 'vector_worker=%s\n' "$(service_image_value vector-worker '{{.Config.Image}}')"
        printf 'vector_worker_image_id=%s\n' "$(service_image_value vector-worker '{{.Image}}')"
    fi
} > "$BACKUP_DIR/previous-images.txt"

echo -e "${BLUE}[1/5] 检查 PostgreSQL...${NC}"
compose up -d postgres redis
wait_for_postgres "${POSTGRES_WAIT_ATTEMPTS:-30}"

echo -e "${BLUE}[2/5] 拉取目标镜像...${NC}"
docker pull "${BACKEND_IMAGE}:${VERSION}"
docker pull "${FRONTEND_IMAGE}:${VERSION}"

echo -e "${BLUE}[3/5] 停止应用写入并备份...${NC}"
STOP_SERVICES=(backend frontend)
[ -z "$(service_container_id vector-worker)" ] || STOP_SERVICES+=(vector-worker)
compose stop "${STOP_SERVICES[@]}"
if ! backup_postgres "$BACKUP_DIR/postgres.dump"; then
    echo -e "${RED}PostgreSQL 备份失败，未执行迁移${NC}" >&2
    restart_previous_apps
    exit 1
fi
if [ -d "$UPLOADS_DIR" ]; then
    if ! tar -czf "$BACKUP_DIR/uploads.tar.gz" -C "$(dirname -- "$UPLOADS_DIR")" "$(basename -- "$UPLOADS_DIR")"; then
        echo -e "${RED}上传目录备份失败，未执行迁移${NC}" >&2
        restart_previous_apps
        exit 1
    fi
else
    printf 'Upload directory did not exist at backup time: %s\n' "$UPLOADS_DIR" > "$BACKUP_DIR/uploads.absent.txt"
fi

export BACKEND_IMAGE FRONTEND_IMAGE IMAGE_VERSION="$VERSION"

echo -e "${BLUE}[4/5] 执行数据库迁移...${NC}"
compose exec -T postgres sh -ec 'psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}" -c "CREATE EXTENSION IF NOT EXISTS vector"'
if ! compose run --rm --no-deps backend msp-migrate; then
    echo -e "${RED}数据库迁移失败，应用保持停止；备份位于 ${BACKUP_DIR}${NC}" >&2
    exit 1
fi

echo -e "${BLUE}[5/5] 启动目标版本...${NC}"
START_SERVICES=(backend frontend)
[ "$VECTOR_WORKER_WAS_RUNNING" = false ] || START_SERVICES+=(vector-worker)
compose up -d "${START_SERVICES[@]}"

persist_image_version
echo -e "${GREEN}=== 更新完成 ===${NC}"
echo -e "${GREEN}备份目录: ${BACKUP_DIR}${NC}"
compose ps
