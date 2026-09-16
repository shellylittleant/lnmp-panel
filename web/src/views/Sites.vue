<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { NButton, NCard, NDataTable, NInput, NForm, NFormItem, NSpace, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
const router = useRouter();
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

async function installNginx() {
  const r = await api<{ job_id: string }>("POST", "/env/nginx/install", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已开始安装 Nginx，任务 " + r.data.job_id);
}

function goRow(r: Row) {
  router.push("/sites/" + r.id);
}
</script>

<template>
  <n-space vertical>
    <n-card title="安装 Nginx">
      <n-button @click="installNginx">从 Ubuntu 源安装 Nginx</n-button>
    </n-card>
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
</template>
