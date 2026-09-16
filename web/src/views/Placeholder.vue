<script setup lang="ts">
import { NAlert, NCard } from "naive-ui";
defineProps<{ title: string }>();
</script>

<template>
  <n-card :title="title">
    <n-alert type="info" title="后续阶段">此模块将在后续阶段提供，当前骨架只保留入口。</n-alert>
  </n-card>
</template>
