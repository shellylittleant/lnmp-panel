<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { NButton, NCard, NForm, NFormItem, NInput, useMessage } from "naive-ui";
import { api } from "../api";

const router = useRouter();
const msg = useMessage();
const username = ref("admin");
const password = ref("");
const code = ref("");
const ticket = ref("");
const needTotp = ref(false);
const loading = ref(false);

async function submit() {
  loading.value = true;
  try {
    if (!needTotp.value) {
      const r = await api<{ need_totp: boolean; ticket?: string }>("POST", "/auth/login", {
        username: username.value,
        password: password.value,
      });
      if (!r.ok) {
        msg.error(r.error.message);
        return;
      }
      if (r.data.need_totp) {
        needTotp.value = true;
        ticket.value = r.data.ticket || "";
        msg.info("请输入 TOTP 验证码");
        return;
      }
    } else {
      const r = await api("POST", "/auth/totp/verify", { ticket: ticket.value, code: code.value });
      if (!r.ok) {
        msg.error(r.error.message);
        return;
      }
    }
    await router.replace("/");
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="wrap">
    <n-card title="LNMP 面板登录" style="width: 380px">
      <n-form @submit.prevent="submit">
        <n-form-item label="用户名">
          <n-input v-model:value="username" autocomplete="username" />
        </n-form-item>
        <n-form-item label="密码">
          <n-input v-model:value="password" type="password" show-password-on="click" autocomplete="current-password" />
        </n-form-item>
        <n-form-item v-if="needTotp" label="TOTP 验证码">
          <n-input v-model:value="code" maxlength="8" />
        </n-form-item>
        <n-button type="primary" block attr-type="submit" :loading="loading">登录</n-button>
      </n-form>
    </n-card>
  </div>
</template>

<style scoped>
.wrap {
  min-height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f5f7fa;
}
</style>
