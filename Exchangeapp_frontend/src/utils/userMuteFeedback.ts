import { h } from 'vue';
import { ElMessage } from 'element-plus';

export const showUserMuteSuccess = (username: string, onUndo: () => void) => {
  ElMessage({
    type: 'success',
    duration: 6000,
    showClose: true,
    message: h('span', { class: 'user-mute-feedback' }, [
      h('span', `Muted @${username}. You won't see posts from this account in your feeds.`),
      h('button', {
        type: 'button',
        class: 'user-mute-feedback__undo',
        onClick: onUndo,
      }, 'Undo'),
    ]),
  });
};

export const showUserUnmuteSuccess = (username: string) => {
  ElMessage.success(`Unmuted @${username}.`);
};

export const showUserMuteError = () => {
  ElMessage.error('Could not update muted accounts. Try again.');
};
