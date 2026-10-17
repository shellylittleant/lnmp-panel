<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NCard, NDataTable } from "naive-ui";
import type { DataTableColumns } from "naive-ui";
import { api } from "../api";

type Row = { id: number; at: string; ip: string; actor: string; action: string; resource: string; detail: string };

const rows = ref<Row[]>([]);
const columns: DataTableColumns<Row> = [
  { title: "时间", key: "at", width: 200 },
  { title: "IP", key: "ip", width: 140 },
  { title: "操作者", key: "actor", width: 100 },
  { title: "动作", key: "action" },
  { title: "资源", key: "resource" },
  { title: "详情", key: "detail", width: 100 },
];

onMounted(async () => {
  const r = await api<{ items: Row[] }>("GET", "/audit?limit=200");
  if (r.ok) rows.value = r.data.items || [];
});
</script>

<template>
  <n-card title="审计日志（不可删除）">
    <n-data-table :columns="columns" :data="rows" :pagination="{ pageSize: 20 }" />
  </n-card>
</template>
