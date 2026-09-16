<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NButton, NCard, NForm, NFormItem, NInput, NSelect, NSpace, useMessage } from "naive-ui";
import QRCode from "qrcode";
import { api } from "../api";

const msg = useMessage();
const bindMode = ref("public");
const ops = ref("");
const oldPw = ref("");
const newPw = ref("");
const totpOn = ref(false);
const totpUri = ref("");
const totpImg = ref("");
const totpCode = ref("");
const totpPw = ref("");

onMounted(async () => {
  const s = await api<{ bind_mode: string; ops_ip_cidrs: string[] }>("GET", "/settings");
  if (s.ok) {
    bindMode.value = s.data.bind_mode;
    ops.value = (s.data.ops_ip_cidrs || []).join(", ");
  }
  const sess = await api<{ totp_enabled: boolean }>("GET", "/auth/session");
  if (sess.ok) totpOn.value = sess.data.totp_enabled;
});

async function save() {
  const cidrs = ops.value
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
  const r = await api<{ need_restart?: boolean }>("PUT", "/settings", { bind_mode: bindMode.value, ops_ip_cidrs: cidrs });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  if (r.data.need_restart) {
    msg.success("监听模式已修改，点击重启面板生效");
  } else {
    msg.success("已保存");
  }
}

async function changePw() {
  const r = await api("POST", "/auth/password", { old_password: oldPw.value, new_password: newPw.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("密码已修改，其他会话已踢出");
  oldPw.value = "";
  newPw.value = "";
}

async function setupTotp() {
  const r = await api<{ secret: string; otpauth_uri: string }>("POST", "/auth/totp/setup", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  totpUri.value = r.data.otpauth_uri;
  totpImg.value = await QRCode.toDataURL(r.data.otpauth_uri);
}

async function enableTotp() {
  const r = await api("POST", "/auth/totp/enable", { code: totpCode.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  totpOn.value = true;
  msg.success("已开启 TOTP");
}

async function disableTotp() {
  const r = await api("POST", "/auth/totp/disable", { password: totpPw.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  totpOn.value = false;
  totpImg.value = "";
  msg.success("已关闭 TOTP");
}

async function restart() {
  const r = await api("POST", "/panel/restart", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("正在重启面板");
}
</script>

<template>
  <n-space vertical size="large">
    <n-card title="访问限制">
      <n-form>
        <n-form-item label="监听模式">
          <n-select
            v-model:value="bindMode"
            :options="[
              { label: '公网', value: 'public' },
              { label: '仅本机（SSH 隧道）', value: 'localhost' },
            ]"
          />
        </n-form-item>
        <n-form-item label="运维 IP / CIDR（逗号分隔，主机角色可留空）">
          <n-input v-model:value="ops" type="textarea" />
          <p class="hint">出口 IP 变化会被拒绝，可 SSH 执行 lnmp-panel whitelist clear 恢复。本机 127.0.0.1 / ::1 始终可访问。</p>
        </n-form-item>
        <n-button type="primary" @click="save">保存</n-button>
        <n-button style="margin-left: 8px" @click="restart">重启面板</n-button>
      </n-form>
    </n-card>
    <n-card title="修改密码">
      <n-form>
        <n-form-item label="旧密码"><n-input v-model:value="oldPw" type="password" /></n-form-item>
        <n-form-item label="新密码（≥12 位，大小写+数字+符号）"><n-input v-model:value="newPw" type="password" /></n-form-item>
        <n-button @click="changePw">修改</n-button>
      </n-form>
    </n-card>
    <n-card title="TOTP 二次验证">
      <p>当前：{{ totpOn ? "已开启" : "未开启（建议开启）" }}</p>
      <n-space vertical>
        <n-button @click="setupTotp">生成绑定密钥</n-button>
        <img v-if="totpImg" :src="totpImg" alt="TOTP 二维码" width="160" height="160" />
        <p v-if="totpUri" class="uri">{{ totpUri }}</p>
        <n-input v-model:value="totpCode" placeholder="验证码" style="max-width: 200px" />
        <n-button type="primary" @click="enableTotp">启用</n-button>
        <n-input v-model:value="totpPw" type="password" placeholder="登录密码（关闭时需要）" style="max-width: 240px" />
        <n-button @click="disableTotp">关闭 TOTP</n-button>
      </n-space>
    </n-card>
  </n-space>
</template>

<style scoped>
.uri {
  word-break: break-all;
  font-size: 12px;
  color: #666;
}
.hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: #666;
  line-height: 1.5;
}
</style>
