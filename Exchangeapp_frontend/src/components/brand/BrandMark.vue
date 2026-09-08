<template>
  <img
    class="brand-mark"
    :src="brandMarkSrc"
    width="64"
    height="64"
    alt=""
    aria-hidden="true"
    draggable="false"
  />
</template>

<script setup lang="ts">
const brandMarkSrc = `${import.meta.env.BASE_URL}favicon.svg`;
</script>

<style scoped>
.brand-mark {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
  user-select: none;
}
</style>
