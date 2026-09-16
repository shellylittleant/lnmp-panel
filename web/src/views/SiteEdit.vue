<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { NAlert, NButton, NCard, NCheckbox, NForm, NFormItem, NInput, NSelect, NSpace, useMessage } from "naive-ui";
import { api } from "../api";

const route = useRoute();
const router = useRouter();
const msg = useMessage();
const id = route.params.id as string;
const drifted = ref(false);
const rawMode = ref(false);
const force = ref(false);
const name = ref("");
const domains = ref("");
const root = ref("");
const php = ref("");
const proxy = ref("");
const conf = ref("");
const http2 = ref(true);
const tpls = ref<{ label: string; value: number }[]>([]);
const nginxTpl = ref<number | null>(null);
const rewriteTpl = ref<number | null>(null);

onMounted(async () => {
  const t = await api<{ items: { id: number; type: string; name: string; origin: string }[] }>("GET", "/templates");
  if (t.ok) {
    tpls.value = (t.data.items || [])
      .filter((x) => x.type === "nginx_site" || x.type === "nginx_rewrite")
      .map((x) => ({ label: `${x.origin === "official" ? "官方" : "用户"} / ${x.type} / ${x.name}`, value: x.id }));
  }
  const r = await api<any>("GET", "/sites/" + id);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  const d = r.data;
  name.value = d.name;
  domains.value = (d.domains || []).join(", ");
  root.value = d.root;
  php.value = d.php_version || "";
  proxy.value = d.proxy_pass || "";
  drifted.value = d.drifted;
  rawMode.value = d.raw_mode;
  http2.value = d.http2;
  nginxTpl.value = d.nginx_template_id;
  rewriteTpl.value = d.rewrite_template_id;
  const n = await api<{ content: string }>("GET", "/sites/" + id + "/nginx");
  if (n.ok) conf.value = n.data.content;
});

async function save() {
  const r = await api("PUT", "/sites/" + id, {
    name: name.value,
    domains: domains.value.split(",").map((x) => x.trim()).filter(Boolean),
    root: root.value,
    php_version: php.value || null,
    proxy_pass: proxy.value,
    http2: http2.value,
    nginx_template_id: nginxTpl.value,
    rewrite_template_id: rewriteTpl.value,
    force: force.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存");
  drifted.value = false;
}

async function saveRaw() {
  const r = await api("PUT", "/sites/" + id + "/nginx", { content: conf.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("原文已保存");
  rawMode.value = true;
}

async function useTpl() {
  const r = await api("POST", "/sites/" + id + "/nginx/use-template", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复模板渲染");
  rawMode.value = false;
}

async function issue() {
  const r = await api("POST", "/sites/" + id + "/ssl/issue", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("证书已签发并写回站点");
}

async function del() {
  const r = await api("DELETE", "/sites/" + id, undefined);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  router.push("/sites");
}
</script>

<template>
  <n-space vertical>
    <n-alert v-if="drifted" type="warning" title="配置漂移">
      磁盘上的配置与面板上次写入不一致，已显示磁盘内容。保存将以你现在提交的内容覆盖磁盘。
    </n-alert>
    <n-alert v-if="rawMode" type="info" title="原文模式">当前为原文模式，表单保存会按模板重写。可点「恢复为模板渲染」。</n-alert>
    <n-card title="站点">
      <n-form>
        <n-form-item label="名称"><n-input v-model:value="name" /></n-form-item>
        <n-form-item label="域名"><n-input v-model:value="domains" /></n-form-item>
        <n-form-item label="根目录"><n-input v-model:value="root" /></n-form-item>
        <n-form-item label="PHP 版本"><n-input v-model:value="php" placeholder="空则静态，如 8.3" /></n-form-item>
        <n-form-item label="反代目标"><n-input v-model:value="proxy" /></n-form-item>
        <n-form-item label="HTTP/2"><n-checkbox v-model:checked="http2" /></n-form-item>
        <n-form-item label="站点模板">
          <n-select v-model:value="nginxTpl" :options="tpls.filter((x) => x.label.includes('nginx_site'))" clearable />
        </n-form-item>
        <n-form-item label="伪静态">
          <n-select v-model:value="rewriteTpl" :options="tpls.filter((x) => x.label.includes('nginx_rewrite'))" clearable />
        </n-form-item>
        <n-checkbox v-model:checked="force">确认覆盖磁盘（漂移时必勾）</n-checkbox>
        <n-space style="margin-top: 12px">
          <n-button type="primary" @click="save">保存表单</n-button>
          <n-button @click="issue">申请/签发证书</n-button>
          <n-button @click="useTpl">恢复为模板渲染</n-button>
          <n-button type="error" @click="del">删除站点</n-button>
        </n-space>
      </n-form>
    </n-card>
    <n-card title="nginx conf 原文">
      <n-input v-model:value="conf" type="textarea" :autosize="{ minRows: 16 }" />
      <n-button style="margin-top: 8px" @click="saveRaw">保存原文</n-button>
    </n-card>
  </n-space>
</template>
