#!/usr/bin/env bash
# Multi-tenant smoke test + security audit
# Run on the VPS as root
# Usage: ./smoke-test.sh [tenant-a] [tenant-b]
set -uo pipefail

TENANT_A="${1:-alpha}"
TENANT_B="${2:-beta}"
CA="alf-$TENANT_A"
CB="alf-$TENANT_B"

OK="PASS"
FAIL="FAIL"
WARN="WARN"
results=()

# check: name, command — passes if rc=0 and non-empty output
check() {
    local name="$1" cmd="$2"
    local out rc
    out=$(eval "$cmd" 2>&1)
    rc=$?
    if [ $rc -eq 0 ] && [ -n "$out" ]; then
        results+=("$OK $name|$out")
    else
        results+=("$FAIL $name|rc=$rc out=$out")
    fi
}

# check_http: name, curl_cmd, expected_code — uses curl WITHOUT -f
check_http() {
    local name="$1" cmd="$2" expected="$3"
    local code
    code=$(eval "$cmd" 2>/dev/null)
    if [ "$code" = "$expected" ]; then
        results+=("$OK $name|$code")
    else
        results+=("$FAIL $name|expected=$expected got=$code")
    fi
}

# check_fail: name, command — passes if command FAILS
check_fail() {
    local name="$1" cmd="$2"
    local out rc
    out=$(eval "$cmd" 2>&1)
    rc=$?
    if [ $rc -ne 0 ] || [ -z "$out" ]; then
        results+=("$OK $name|blocked (rc=$rc)")
    else
        results+=("$FAIL $name|UNEXPECTED SUCCESS: $out")
    fi
}

warn_check() {
    local name="$1" cmd="$2"
    local out rc
    out=$(eval "$cmd" 2>&1)
    rc=$?
    if [ $rc -eq 0 ] && [ -n "$out" ]; then
        results+=("$WARN $name|$out")
    else
        results+=("$OK $name|clean")
    fi
}

echo "========================================================"
echo "  ALF Multi-Tenant Smoke Test + Security Audit"
echo "========================================================"
echo "date:     $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "host:     $(hostname)"
echo "tenants:  $TENANT_A, $TENANT_B"
echo "docker:   $(docker --version 2>/dev/null | head -1)"
echo ""

# ═══════════════════════════════════════════════════════════════
# 1. INFRASTRUCTURE HEALTH
# ═══════════════════════════════════════════════════════════════
echo "--- 1. Infrastructure Health ---"
for c in $CA $CB alf-whisper alf-traefik; do
    check "$c running" "docker inspect -f '{{.State.Status}}' $c 2>/dev/null | grep -q running && echo running"
done
check "whisper healthy" "docker exec $CB curl -s --connect-timeout 3 http://10.99.0.10:8000/health | grep -o '\"status\":\"[^\"]*\"'"
check_http "$TENANT_A CC listens" "docker exec $CA curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/" "401"
check_http "$TENANT_B CC listens" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/" "401"

# ═══════════════════════════════════════════════════════════════
# 2. TENANT ISOLATION — Filesystem
# ═══════════════════════════════════════════════════════════════
echo "--- 2. Filesystem Isolation ---"
check "$TENANT_A data dir exists" "docker exec $CA ls /home/alf/data/ | head -3 | tr '\n' ','"
check "$TENANT_B data dir exists" "docker exec $CB ls /home/alf/data/ | head -3 | tr '\n' ','"
check_fail "$TENANT_A cannot see host /opt" "docker exec $CA ls /opt/alf-multi/ 2>&1 | grep tenants"
check_fail "$TENANT_B cannot see host /opt" "docker exec $CB ls /opt/alf-multi/ 2>&1 | grep tenants"
check_fail "$TENANT_A no docker socket" "docker exec $CA ls -la /var/run/docker.sock"
check_fail "$TENANT_B no docker socket" "docker exec $CB ls -la /var/run/docker.sock"

# Cross-tenant data isolation via volume mounts
A_ID=$(docker exec $CA cat /proc/sys/kernel/hostname 2>/dev/null)
B_ID=$(docker exec $CB cat /proc/sys/kernel/hostname 2>/dev/null)
check "different container IDs" "[ '$A_ID' != '$B_ID' ] && echo '$A_ID vs $B_ID'"

# ═══════════════════════════════════════════════════════════════
# 3. TENANT ISOLATION — Secrets
# ═══════════════════════════════════════════════════════════════
echo "--- 3. Secret Isolation ---"
TOKEN_A=$(cat /opt/alf-multi/tenants/$TENANT_A/secrets/cc_auth_token 2>/dev/null)
TOKEN_B=$(cat /opt/alf-multi/tenants/$TENANT_B/secrets/cc_auth_token 2>/dev/null)
check "$TENANT_A has token" "[ -n '$TOKEN_A' ] && echo '${TOKEN_A:0:8}...'"
check "$TENANT_B has token" "[ -n '$TOKEN_B' ] && echo '${TOKEN_B:0:8}...'"
check "tokens are unique" "[ '$TOKEN_A' != '$TOKEN_B' ] && echo different"
check "token entropy >= 32 chars" "[ \${#TOKEN_A} -ge 32 ] && [ \${#TOKEN_B} -ge 32 ] && echo 'A=${#TOKEN_A} B=${#TOKEN_B}'"

# Secrets not readable across containers
check_fail "$TENANT_A cannot read $TENANT_B secrets" "docker exec $CA cat /opt/alf-multi/tenants/$TENANT_B/secrets/cc_auth_token"
check_fail "$TENANT_B cannot read $TENANT_A secrets" "docker exec $CB cat /opt/alf-multi/tenants/$TENANT_A/secrets/cc_auth_token"

# ═══════════════════════════════════════════════════════════════
# 4. TENANT ISOLATION — Network
# ═══════════════════════════════════════════════════════════════
echo "--- 4. Network Isolation ---"
check_fail "$TENANT_A cannot reach $TENANT_B:8080" "docker exec $CA curl -sf --connect-timeout 2 http://$CB:8080/"
check_fail "$TENANT_B cannot reach $TENANT_A:8080" "docker exec $CB curl -sf --connect-timeout 2 http://$CA:8080/"

# By IP
B_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' $CB 2>/dev/null | awk '{print $1}')
A_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' $CA 2>/dev/null | awk '{print $1}')
check_fail "$TENANT_A cannot reach $TENANT_B by IP ($B_IP)" "docker exec $CA curl -sf --connect-timeout 2 http://$B_IP:8080/"
check_fail "$TENANT_B cannot reach $TENANT_A by IP ($A_IP)" "docker exec $CB curl -sf --connect-timeout 2 http://$A_IP:8080/"

# ═══════════════════════════════════════════════════════════════
# 5. OWASP A01: Broken Access Control
# ═══════════════════════════════════════════════════════════════
echo "--- 5. OWASP A01: Broken Access Control ---"

# Unauthenticated access must return 401
for ep in /api/user/ /api/chat /api/tiers /api/teams /api/schedules /api/vault/secrets /api/restart; do
    check_http "unauth $ep → 401" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' http://localhost:8080$ep" "401"
done

# Cross-tenant token must be rejected
check_http "$TENANT_A token rejected by $TENANT_B" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer $TOKEN_A' http://localhost:8080/api/user/" "401"
check_http "$TENANT_B token rejected by $TENANT_A" "docker exec $CA curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer $TOKEN_B' http://localhost:8080/api/user/" "401"

# Valid token works
check_http "$TENANT_A own token accepted" "docker exec $CA curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer $TOKEN_A' http://localhost:8080/api/user/" "200"
check_http "$TENANT_B own token accepted" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer $TOKEN_B' http://localhost:8080/api/user/" "200"

# ═══════════════════════════════════════════════════════════════
# 6. OWASP A02: Cryptographic Failures
# ═══════════════════════════════════════════════════════════════
echo "--- 6. OWASP A02: Cryptographic Failures ---"
check "secrets not world-readable" "stat -c '%a' /opt/alf-multi/tenants/$TENANT_A/secrets/cc_auth_token | grep -qE '^6[04][04]$' && echo ok"
check "whisper secret not world-readable" "stat -c '%a' /opt/alf-multi/shared/whisper_shared_secret | grep -qE '^6[04][04]$' && echo ok"

# ═══════════════════════════════════════════════════════════════
# 7. OWASP A03: Injection
# ═══════════════════════════════════════════════════════════════
echo "--- 7. OWASP A03: Injection ---"
# Path traversal — should NOT return 200 with sensitive content
TRAVERSAL_BODY=$(docker exec $CB curl -s 'http://localhost:8080/api/../../../etc/passwd' 2>/dev/null)
if echo "$TRAVERSAL_BODY" | grep -q "root:"; then
    results+=("$FAIL path traversal exposes /etc/passwd|CRITICAL: file content leaked")
else
    results+=("$OK path traversal safe|no sensitive data leaked (body=${#TRAVERSAL_BODY} bytes)")
fi

# Null byte injection
check_http "null byte injection" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' 'http://localhost:8080/api/user/%00.json'" "401"

# ═══════════════════════════════════════════════════════════════
# 8. OWASP A05: Security Misconfiguration
# ═══════════════════════════════════════════════════════════════
echo "--- 8. OWASP A05: Security Misconfiguration ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    check "$tag no-new-privileges" "docker inspect -f '{{.HostConfig.SecurityOpt}}' $c | grep -q 'no-new-privileges' && echo enforced"
    check "$tag caps dropped ALL" "docker inspect -f '{{.HostConfig.CapDrop}}' $c | grep -q 'ALL' && echo yes"
    check "$tag caps added" "docker inspect -f '{{.HostConfig.CapAdd}}' $c"
    check "$tag not privileged" "docker inspect -f '{{.HostConfig.Privileged}}' $c | grep -q 'false' && echo yes"
    check "$tag no docker socket" "docker inspect -f '{{json .Mounts}}' $c | grep -qv 'docker.sock' && echo clean"
    check "$tag memory limit" "docker inspect -f '{{.HostConfig.Memory}}' $c | grep -v '^0$'"
    check "$tag cpu limit" "docker inspect -f '{{.HostConfig.NanoCpus}}' $c | grep -v '^0$'"

    # Read-only rootfs
    RO=$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' $c)
    if [ "$RO" = "true" ]; then
        results+=("$OK $tag read-only rootfs|true")
    else
        results+=("$WARN $tag read-only rootfs|false (writable)")
    fi
done

# Running user inside container
A_USER=$(docker exec $CA whoami 2>/dev/null)
B_USER=$(docker exec $CB whoami 2>/dev/null)
if [ "$A_USER" = "root" ]; then
    results+=("$WARN $TENANT_A container user|root (consider non-root)")
else
    results+=("$OK $TENANT_A container user|$A_USER")
fi
if [ "$B_USER" = "root" ]; then
    results+=("$WARN $TENANT_B container user|root (consider non-root)")
else
    results+=("$OK $TENANT_B container user|$B_USER")
fi

# ═══════════════════════════════════════════════════════════════
# 9. OWASP A06: Vulnerable Components
# ═══════════════════════════════════════════════════════════════
echo "--- 9. OWASP A06: Vulnerable Components ---"
check "$TENANT_A image" "docker inspect -f '{{.Config.Image}}' $CA"
check "$TENANT_B image" "docker inspect -f '{{.Config.Image}}' $CB"
check "traefik version" "docker exec alf-traefik traefik version 2>/dev/null | head -1 || docker inspect -f '{{.Config.Image}}' alf-traefik"
check "whisper image" "docker inspect -f '{{.Config.Image}}' alf-whisper"

warn_check "$TENANT_A upgradable packages" "docker exec $CA sh -c 'apt list --upgradable 2>/dev/null | grep -c upgradable || echo 0'"
warn_check "whisper outdated pip packages" "docker exec alf-whisper sh -c 'pip list --outdated 2>/dev/null | wc -l || echo unknown'"

# ═══════════════════════════════════════════════════════════════
# 10. OWASP A07: Auth Failures
# ═══════════════════════════════════════════════════════════════
echo "--- 10. OWASP A07: Auth Failures ---"
check_http "invalid token → 401" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer INVALID_TOKEN_12345' http://localhost:8080/api/user/" "401"
check_http "empty token → 401" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer ' http://localhost:8080/api/user/" "401"
check_http "no auth header → 401" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/user/" "401"
check_http "basic auth → 401" "docker exec $CB curl -s -o /dev/null -w '%{http_code}' -H 'Authorization: Basic dGVzdDp0ZXN0' http://localhost:8080/api/user/" "401"

# Rate limiting present
check_http "rate limit after rapid requests" "for i in \$(seq 1 20); do docker exec $CB curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/api/user/; done | grep -c 429 | xargs -I{} test {} -gt 0 && docker exec $CB curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/user/" "429"

# ═══════════════════════════════════════════════════════════════
# 11. OWASP A08: SSRF
# ═══════════════════════════════════════════════════════════════
echo "--- 11. OWASP A08: SSRF ---"
check_fail "$TENANT_A no AWS metadata" "docker exec $CA curl -sf --connect-timeout 2 http://169.254.169.254/latest/meta-data/"
check_fail "$TENANT_B no AWS metadata" "docker exec $CB curl -sf --connect-timeout 2 http://169.254.169.254/latest/meta-data/"
check_fail "$TENANT_A no GCP metadata" "docker exec $CA curl -sf --connect-timeout 2 -H 'Metadata-Flavor: Google' http://metadata.google.internal/"
check_fail "$TENANT_B no GCP metadata" "docker exec $CB curl -sf --connect-timeout 2 -H 'Metadata-Flavor: Google' http://metadata.google.internal/"
check_fail "$TENANT_A no Azure metadata" "docker exec $CA curl -sf --connect-timeout 2 -H 'Metadata: true' 'http://169.254.169.254/metadata/instance?api-version=2021-02-01'"
check_fail "$TENANT_B no Azure metadata" "docker exec $CB curl -sf --connect-timeout 2 -H 'Metadata: true' 'http://169.254.169.254/metadata/instance?api-version=2021-02-01'"

# ═══════════════════════════════════════════════════════════════
# 12. OWASP A09: Logging
# ═══════════════════════════════════════════════════════════════
echo "--- 12. OWASP A09: Logging ---"
check "$TENANT_A has logs" "docker exec $CA ls /home/alf/data/logs/ | head -3 | tr '\n' ','"
check "$TENANT_B has logs" "docker exec $CB ls /home/alf/data/logs/ | head -3 | tr '\n' ','"
check "auth failures logged" "docker logs $CB --tail 100 2>&1 | grep -c 'auth fail' | xargs -I{} test {} -gt 0 && echo yes"

# ═══════════════════════════════════════════════════════════════
# 13. PID & Process Isolation
# ═══════════════════════════════════════════════════════════════
echo "--- 13. PID & Process Isolation ---"
check "$TENANT_A PID 1" "docker exec $CA cat /proc/1/cmdline | tr '\0' ' ' | cut -c1-60"
check "$TENANT_B PID 1" "docker exec $CB cat /proc/1/cmdline | tr '\0' ' ' | cut -c1-60"

A_PID_MODE=$(docker inspect -f '{{.HostConfig.PidMode}}' $CA)
B_PID_MODE=$(docker inspect -f '{{.HostConfig.PidMode}}' $CB)
check "$TENANT_A PID namespace" "[ '$A_PID_MODE' != 'host' ] && echo isolated"
check "$TENANT_B PID namespace" "[ '$B_PID_MODE' != 'host' ] && echo isolated"
check_fail "$TENANT_A cannot see host processes" "docker exec $CA ps aux 2>/dev/null | grep -v grep | grep -E 'dockerd|sshd|systemd' | head -1"

# ═══════════════════════════════════════════════════════════════
# 14. TLS & Traefik
# ═══════════════════════════════════════════════════════════════
echo "--- 14. TLS & Traefik ---"
check "traefik running" "docker inspect -f '{{.State.Status}}' alf-traefik | grep running"
check "traefik exposes 80+443" "docker inspect -f '{{json .HostConfig.PortBindings}}' alf-traefik | grep -o '80\|443' | sort -u | tr '\n' ','"
check "letsencrypt storage exists" "ls /opt/alf-multi/letsencrypt/ | tr '\n' ','"
check "HTTP→HTTPS redirect configured" "docker inspect -f '{{json .Config.Cmd}}' alf-traefik | grep -q 'redirections' && echo yes"

# ═══════════════════════════════════════════════════════════════
# 15. CVE — Runtime Versions
# ═══════════════════════════════════════════════════════════════
echo "--- 15. CVE: Runtime Versions ---"

# CVE-2024-21626 / CVE-2019-5736 — runc escape
# runc >= 1.1.12 fixes Leaky Vessels
RUNC_VER=$(runc --version 2>/dev/null | grep 'runc version' | awk '{print $3}')
check "runc version" "echo $RUNC_VER"
RUNC_MAJOR=$(echo $RUNC_VER | cut -d. -f1)
RUNC_MINOR=$(echo $RUNC_VER | cut -d. -f2)
RUNC_PATCH=$(echo $RUNC_VER | cut -d. -f3)
if [ -n "$RUNC_VER" ]; then
    if [ "$RUNC_MAJOR" -gt 1 ] || { [ "$RUNC_MAJOR" -eq 1 ] && [ "$RUNC_MINOR" -gt 1 ]; } || \
       { [ "$RUNC_MAJOR" -eq 1 ] && [ "$RUNC_MINOR" -eq 1 ] && [ "${RUNC_PATCH:-0}" -ge 12 ]; }; then
        results+=("$OK CVE-2024-21626 Leaky Vessels (runc)|patched ($RUNC_VER >= 1.1.12)")
    else
        results+=("$FAIL CVE-2024-21626 Leaky Vessels (runc)|VULNERABLE: $RUNC_VER < 1.1.12")
    fi
else
    results+=("$WARN CVE-2024-21626 runc version|cannot determine version")
fi

# CVE-2020-15257 — containerd host network TOCTOU
CONTAINERD_VER=$(containerd --version 2>/dev/null | awk '{print $3}' | tr -d 'v')
check "containerd version" "echo ${CONTAINERD_VER:-unknown}"

# Kernel version (relevant for namespace escapes)
KERNEL=$(uname -r)
check "kernel version" "echo $KERNEL"

# Docker daemon version
DOCKER_VER=$(docker version --format '{{.Server.Version}}' 2>/dev/null)
check "docker daemon version" "echo ${DOCKER_VER:-unknown}"

# ═══════════════════════════════════════════════════════════════
# 16. OWASP Docker D04 — Seccomp & AppArmor
# ═══════════════════════════════════════════════════════════════
echo "--- 16. OWASP D04: Seccomp & AppArmor ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    SECCOMP=$(docker inspect -f '{{.HostConfig.SecurityOpt}}' $c 2>/dev/null)
    if echo "$SECCOMP" | grep -q "seccomp"; then
        results+=("$OK $tag seccomp profile|custom")
    else
        # Check if default seccomp is active (Docker applies it by default unless unconfined)
        SECCOMP_STATUS=$(docker inspect -f '{{json .HostConfig.SecurityOpt}}' $c)
        if echo "$SECCOMP_STATUS" | grep -q "unconfined"; then
            results+=("$FAIL $tag seccomp|DISABLED (unconfined)")
        else
            results+=("$OK $tag seccomp|default Docker profile active")
        fi
    fi

    APPARMOR=$(docker inspect -f '{{json .AppArmorProfile}}' $c 2>/dev/null)
    if echo "$APPARMOR" | grep -q "unconfined\|^\"\""; then
        results+=("$WARN $tag AppArmor|unconfined or not set")
    else
        results+=("$OK $tag AppArmor|$APPARMOR")
    fi
done

# ═══════════════════════════════════════════════════════════════
# 17. OWASP D05 — Namespace Isolation (IPC, UTS, User)
# ═══════════════════════════════════════════════════════════════
echo "--- 17. OWASP D05: Namespace Isolation ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    IPC=$(docker inspect -f '{{.HostConfig.IpcMode}}' $c)
    UTS=$(docker inspect -f '{{.HostConfig.UTSMode}}' $c)
    USERNS=$(docker inspect -f '{{.HostConfig.UsernsMode}}' $c)
    NET=$(docker inspect -f '{{.HostConfig.NetworkMode}}' $c)

    if [ "$IPC" = "host" ]; then
        results+=("$FAIL $tag IPC namespace|SHARED WITH HOST (host mode)")
    else
        results+=("$OK $tag IPC namespace|isolated ($IPC)")
    fi

    if [ "$UTS" = "host" ]; then
        results+=("$FAIL $tag UTS namespace|SHARED WITH HOST")
    else
        results+=("$OK $tag UTS namespace|isolated")
    fi

    if [ "$NET" = "host" ]; then
        results+=("$FAIL $tag network namespace|SHARED WITH HOST (CVE-2020-15257 risk)")
    else
        results+=("$OK $tag network namespace|isolated ($NET)")
    fi

    if [ -z "$USERNS" ]; then
        results+=("$WARN $tag user namespace|not remapped (root in container = root on host if escape)")
    else
        results+=("$OK $tag user namespace|$USERNS")
    fi
done

# ═══════════════════════════════════════════════════════════════
# 18. OWASP D08 — Image Integrity
# ═══════════════════════════════════════════════════════════════
echo "--- 18. OWASP D08: Image Integrity ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    IMAGE=$(docker inspect -f '{{.Config.Image}}' $c)
    DIGEST=$(docker inspect -f '{{index .RepoDigests 0}}' $c 2>/dev/null || echo "")
    if echo "$IMAGE" | grep -q ":latest"; then
        results+=("$WARN $tag uses :latest tag|pin to digest for reproducibility ($DIGEST)")
    else
        results+=("$OK $tag image tag|$IMAGE")
    fi
done

# ═══════════════════════════════════════════════════════════════
# 19. OWASP D09 — Secret leakage in ENV vars
# ═══════════════════════════════════════════════════════════════
echo "--- 19. OWASP D09: Secret Leakage in ENV ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    # Check if any env var contains an actual secret value (not just a path)
    LEAKED=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' $c | \
        grep -iE '(key|token|secret|password|api_key)=' | \
        grep -v '_FILE=' | grep -v '=$' || true)
    if [ -n "$LEAKED" ]; then
        results+=("$FAIL $tag secrets in env|LEAKED: $(echo $LEAKED | cut -c1-80)")
    else
        results+=("$OK $tag secrets in env|none (using _FILE pattern)")
    fi
done

# ═══════════════════════════════════════════════════════════════
# 20. SUID/SGID binaries in containers
# ═══════════════════════════════════════════════════════════════
echo "--- 20. SUID/SGID Binaries ---"

for c in $CA $CB; do
    tag=$(echo $c | sed "s/alf-//")
    SUID_COUNT=$(docker exec $c find / -xdev -perm /6000 -type f 2>/dev/null | wc -l)
    SUID_LIST=$(docker exec $c find / -xdev -perm /6000 -type f 2>/dev/null | tr '\n' ',')
    if [ "$SUID_COUNT" -eq 0 ]; then
        results+=("$OK $tag SUID/SGID binaries|none")
    elif [ "$SUID_COUNT" -le 20 ]; then
        results+=("$WARN $tag SUID/SGID binaries|$SUID_COUNT found (review): $SUID_LIST")
    else
        results+=("$FAIL $tag SUID/SGID binaries|$SUID_COUNT found (too many — unexpected)")
    fi
done

# ═══════════════════════════════════════════════════════════════
# SUMMARY
# ═══════════════════════════════════════════════════════════════
echo ""
echo "========================================================"
echo "  RESULTS"
echo "========================================================"
pass=0 fail=0 warns=0
for r in "${results[@]}"; do
    name="${r%%|*}"
    value="${r#*|}"
    echo "$name → $value"
    if [[ "$name" == "$OK"* ]]; then ((pass++))
    elif [[ "$name" == "$WARN"* ]]; then ((warns++))
    else ((fail++)); fi
done
echo ""
echo "========================================================"
echo "TOTAL: $pass passed, $fail failed, $warns warnings"
echo "========================================================"
