<template>
  <div ref="rootRef" class="mobile-account-menu" @focusout="handleFocusOut">
    <button
      ref="triggerRef"
      class="mobile-account-menu__trigger"
      type="button"
      aria-label="Account menu"
      aria-haspopup="menu"
      :aria-expanded="isOpen"
      @click="toggleMenu"
    >
      <AppIcon name="more" :size="22" />
    </button>

    <div v-if="isOpen" ref="menuRef" class="mobile-account-menu__popover" role="menu">
      <RouterLink
        class="mobile-account-menu__item"
        role="menuitem"
        :to="{ name: 'History' }"
        @click="closeMenu()"
      >
        <AppIcon name="history" :size="18" />
        <span>History</span>
      </RouterLink>
      <button
        class="mobile-account-menu__item mobile-account-menu__item--logout"
        type="button"
        role="menuitem"
        @click="handleLogoutClick"
      >
        <AppIcon name="logout" :size="18" />
        <span>Log out</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import { useLogout } from '../../composables/useLogout';
import AppIcon from '../icons/AppIcon.vue';

const { handleLogout } = useLogout();
const rootRef = ref<HTMLElement | null>(null);
const triggerRef = ref<HTMLButtonElement | null>(null);
const menuRef = ref<HTMLElement | null>(null);
const isOpen = ref(false);

const removeDocumentListeners = () => {
  document.removeEventListener('pointerdown', handleDocumentPointerDown);
  document.removeEventListener('keydown', handleDocumentKeydown);
};

const closeMenu = (restoreFocus = false) => {
  const wasOpen = isOpen.value;
  isOpen.value = false;
  removeDocumentListeners();
  if (restoreFocus && wasOpen) {
    triggerRef.value?.focus();
  }
};

const handleDocumentPointerDown = (event: PointerEvent) => {
  const target = event.target;
  if (!(target instanceof Node) || rootRef.value?.contains(target)) {
    return;
  }
  closeMenu();
};

const handleFocusOut = (event: FocusEvent) => {
  if (!isOpen.value) {
    return;
  }
  const nextTarget = event.relatedTarget;
  if (nextTarget instanceof Node && rootRef.value?.contains(nextTarget)) {
    return;
  }
  closeMenu();
};

const handleDocumentKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') {
    event.preventDefault();
    closeMenu(true);
  }
};

const addDocumentListeners = () => {
  document.addEventListener('pointerdown', handleDocumentPointerDown);
  document.addEventListener('keydown', handleDocumentKeydown);
};

const toggleMenu = () => {
  if (isOpen.value) {
    closeMenu(true);
    return;
  }
  isOpen.value = true;
  addDocumentListeners();
};

const handleLogoutClick = () => {
  closeMenu();
  handleLogout();
};

watch(isOpen, (open) => {
  if (!open) {
    removeDocumentListeners();
  }
});

onBeforeUnmount(() => {
  removeDocumentListeners();
});
</script>

<style scoped>
.mobile-account-menu {
  display: none;
}

@media (max-width: 799px) {
  .mobile-account-menu {
    position: relative;
    display: block;
    flex: 0 0 auto;
  }

  .mobile-account-menu__trigger {
    display: grid;
    width: 44px;
    height: 44px;
    place-items: center;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: var(--color-text-secondary);
    cursor: pointer;
  }

  .mobile-account-menu__trigger:hover,
  .mobile-account-menu__trigger:focus-visible {
    background: var(--color-surface-subtle);
    color: var(--color-text);
  }

  .mobile-account-menu__popover {
    position: absolute;
    top: calc(100% + var(--space-1));
    right: 0;
    z-index: 30;
    display: grid;
    min-width: 148px;
    gap: 2px;
    padding: var(--space-1);
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-sm);
    background: var(--color-surface);
    box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12);
  }

  .mobile-account-menu__item {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 40px;
    border: 0;
    border-radius: calc(var(--radius-sm) - 2px);
    padding: 0 var(--space-3);
    background: transparent;
    color: var(--color-text);
    cursor: pointer;
    font: inherit;
    font-size: 13px;
    text-align: left;
    text-decoration: none;
    white-space: nowrap;
  }

  .mobile-account-menu__item:hover,
  .mobile-account-menu__item:focus-visible {
    background: var(--color-surface-subtle);
    color: var(--color-accent);
  }

  .mobile-account-menu__item--logout {
    color: var(--color-danger);
  }
}
</style>
