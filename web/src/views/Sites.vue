<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { NAlert, NButton, NCard, NDataTable, NInput, NForm, NFormItem, NSpace, NTabPane, NTabs, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";
import Templates from "./Templates.vue";

const msg = useMessage();
const router = useRouter();
const route = useRoute();
const tab = computed({
  get: () => (route.query.tab === "tpl" ? "tpl" : "sites"),
  set: (v: string) => router.replace({ query: v === "sites" ? {} : { tab: v } }),
});
const nginxOn = ref(true);
type Row = { id: number; site_key: string; name: string; domains: string[]; enabled: boolean; drifted: boolean };
const rows = ref<Row[]>([]);
const key = ref("");
const name = ref("");
const domains = ref("");
const columns: DataTableColumns<Row> = [
  { title: "标识", key: "site_key" },
  { title: "名称", key: "name" },
  { title: "域名", key: "domains", render: (r) => (r.domains || []).join(", ") },
  { title: "启用", key: "enabled", render: (r) => (r.enabled ? "是" : "否") },
  { title: "漂移", key: "drifted", render: (r) => (r.drifted ? "是" : "") },
];

async function load() {
  const r = await api<{ items: Row[] }>("GET", "/sites");
  if (r.ok) rows.value = r.data.items || [];
  const e = await api<{ nginx_installed: boolean }>("GET", "/env");
  if (e.ok) nginxOn.value = !!e.data.nginx_installed;
}

onMounted(load);

async function create() {
  const r = await api("POST", "/sites", {
    site_key: key.value,
    name: name.value,
    domains: domains.value.split(",").map((x) => x.trim()).filter(Boolean),
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已创建");
  await load();
}

function goRow(r: Row) {
  router.push("/sites/" + r.id);
}
</script>

<template>
  <n-tabs v-model:value="tab" type="line">
    <n-tab-pane name="sites" tab="站点">
  <n-space vertical>
    <n-alert v-if="!nginxOn" type="warning" title="尚未安装 Nginx">
      请到设置 → 环境安装完成后再建站。
      <n-button size="small" style="margin-left: 8px" @click="router.push({ path: '/settings', query: { tab: 'env' } })">去环境安装</n-button>
    </n-alert>
    <n-card title="新建站点">
      <n-form inline>
        <n-form-item label="标识"><n-input v-model:value="key" placeholder="blog" /></n-form-item>
        <n-form-item label="名称"><n-input v-model:value="name" /></n-form-item>
        <n-form-item label="域名"><n-input v-model:value="domains" placeholder="example.com, www.example.com" /></n-form-item>
        <n-button type="primary" @click="create">创建</n-button>
      </n-form>
    </n-card>
    <n-card title="站点列表">
      <n-data-table :columns="columns" :data="rows" :row-props="(r: Row) => ({ style: 'cursor:pointer', onClick: () => goRow(r) })" />
    </n-card>
  </n-space>
    </n-tab-pane>
    <n-tab-pane name="tpl" tab="模板库">
      <Templates />
    </n-tab-pane>
  </n-tabs>
</template>
