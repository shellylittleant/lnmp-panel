<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NButton, NCard, NDataTable, NInput, NSelect, NSpace, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
type Row = { id: number; type: string; name: string; origin: string; content: string };
const rows = ref<Row[]>([]);
const current = ref<Row | null>(null);
const newType = ref("nginx_site");
const newName = ref("");
const newContent = ref("");
const columns: DataTableColumns<Row> = [
  { title: "类型", key: "type", width: 140 },
  { title: "名称", key: "name" },
  { title: "来源", key: "origin", width: 80 },
];

async function load() {
  const r = await api<{ items: Row[] }>("GET", "/templates");
  if (r.ok) rows.value = r.data.items || [];
}
onMounted(load);

async function open(r: Row) {
  const g = await api<Row>("GET", "/templates/" + r.id);
  if (g.ok) current.value = g.data;
}

async function create() {
  if (!newName.value.trim() || !newContent.value.trim()) {
    msg.error("请填写名称和内容");
    return;
  }
  const r = await api<Row>("POST", "/templates", { type: newType.value, name: newName.value.trim(), content: newContent.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已新建模板");
  newName.value = "";
  newContent.value = "";
  await load();
}

async function remove() {
  if (!current.value || current.value.origin !== "user") return;
  if (!window.confirm("删除用户模板「" + current.value.name + "」。正在使用的模板会拒绝删除。")) return;
  const r = await api("DELETE", "/templates/" + current.value.id, {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已删除");
  current.value = null;
  await load();
}

async function save() {
  if (!current.value) return;
  const r = await api("PUT", "/templates/" + current.value.id, {
    name: current.value.name,
    content: current.value.content,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存");
}
</script>

<template>
  <n-space vertical>
    <n-card title="新建模板">
      <n-space vertical>
        <n-select v-model:value="newType" :options="[
          { label: '站点 nginx', value: 'nginx_site' },
          { label: '伪静态', value: 'nginx_rewrite' },
          { label: 'SSL', value: 'nginx_ssl' },
          { label: '防火墙', value: 'nftables' },
          { label: 'logrotate', value: 'logrotate' },
          { label: 'php.ini', value: 'php_ini' },
          { label: 'php-fpm pool', value: 'php_fpm_pool' },
          { label: 'my.cnf', value: 'mysql_cnf' },
          { label: '跳转', value: 'nginx_redirect' },
        ]" />
        <n-input v-model:value="newName" placeholder="名称" />
        <n-input v-model:value="newContent" type="textarea" placeholder="内容" :autosize="{ minRows: 4 }" />
        <n-button type="primary" @click="create">新建</n-button>
      </n-space>
    </n-card>
    <n-card title="模板库">
      <n-data-table :columns="columns" :data="rows" :row-props="(r: Row) => ({ style: 'cursor:pointer', onClick: () => open(r) })" />
    </n-card>
    <n-card v-if="current" :title="(current.origin === 'official' ? '官方（只读内容）' : '用户模板') + ' ' + current.name">
      <n-input v-model:value="current.name" :disabled="current.origin === 'official'" />
      <n-input v-model:value="current.content" type="textarea" :autosize="{ minRows: 12 }" :disabled="current.origin === 'official'" style="margin-top: 8px" />
      <n-space v-if="current.origin === 'user'" style="margin-top: 8px">
        <n-button type="primary" @click="save">保存</n-button>
        <n-button type="error" @click="remove">删除</n-button>
      </n-space>
    </n-card>
  </n-space>
</template>
