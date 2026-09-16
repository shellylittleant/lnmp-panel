<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NButton, NCard, NDataTable, NInput, NSpace, useMessage } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
type Row = { id: number; type: string; name: string; origin: string; content: string };
const rows = ref<Row[]>([]);
const current = ref<Row | null>(null);
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
    <n-card title="模板库">
      <n-data-table :columns="columns" :data="rows" :row-props="(r: Row) => ({ style: 'cursor:pointer', onClick: () => open(r) })" />
    </n-card>
    <n-card v-if="current" :title="(current.origin === 'official' ? '官方（只读内容）' : '用户模板') + ' ' + current.name">
      <n-input v-model:value="current.name" :disabled="current.origin === 'official'" />
      <n-input v-model:value="current.content" type="textarea" :autosize="{ minRows: 12 }" :disabled="current.origin === 'official'" style="margin-top: 8px" />
      <n-button v-if="current.origin === 'user'" style="margin-top: 8px" type="primary" @click="save">保存</n-button>
    </n-card>
  </n-space>
</template>
