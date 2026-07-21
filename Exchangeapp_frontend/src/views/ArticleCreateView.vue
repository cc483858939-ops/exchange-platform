<template>
  <el-container>
    <el-main class="compose-main">
      <section class="compose-panel">
        <header class="compose-header">
          <div>
            <h1>发布文章</h1>
            <p>填写正文并上传一张封面图</p>
          </div>
        </header>

        <el-form class="article-form" :model="form" label-position="top" @submit.prevent="submitArticle">
          <el-form-item label="标题">
            <el-input v-model="form.title" maxlength="80" show-word-limit placeholder="输入文章标题" />
          </el-form-item>

          <el-form-item label="摘要">
            <el-input v-model="form.preview" maxlength="180" show-word-limit placeholder="输入列表中展示的简短摘要" />
          </el-form-item>

          <el-form-item label="正文">
            <el-input
              v-model="form.content"
              type="textarea"
              :rows="12"
              maxlength="10000"
              show-word-limit
              placeholder="输入文章正文"
            />
          </el-form-item>

          <el-form-item label="封面图">
            <label class="cover-picker" for="article-cover-input">
              <input
                id="article-cover-input"
                class="cover-input"
                type="file"
                accept="image/jpeg,image/png,image/webp"
                @change="handleCoverChange"
              />
              <span v-if="!coverPreview" class="cover-empty">选择 jpg、png 或 webp 图片</span>
              <img v-else class="cover-preview" :src="coverPreview" alt="文章封面预览" />
            </label>
            <el-button v-if="coverFile" text type="danger" @click="clearCover">移除封面</el-button>
          </el-form-item>

          <div class="form-actions">
            <el-button @click="cancelCreate">取消</el-button>
            <el-button type="primary" native-type="submit" :loading="submitting">发布</el-button>
          </div>
        </el-form>
      </section>
    </el-main>
  </el-container>
</template>

<script setup lang="ts">
import { reactive, ref, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';
import { ElMessage } from 'element-plus';
import axios from '../axios';
import type { Article } from '../types/Article';

type UploadArticleCoverResponse = {
  cover_image_url: string;
};

type CreateArticlePayload = {
  title: string;
  preview: string;
  content: string;
  cover_image_url?: string;
};

const maxCoverBytes = 5 * 1024 * 1024;
const allowedCoverTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);

const router = useRouter();
const submitting = ref(false);
const coverFile = ref<File | null>(null);
const coverPreview = ref('');
const form = reactive({
  title: '',
  preview: '',
  content: '',
});

const revokeCoverPreview = () => {
  if (coverPreview.value) {
    URL.revokeObjectURL(coverPreview.value);
  }
};

const handleCoverChange = (event: Event) => {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) {
    clearCover();
    return;
  }

  if (!allowedCoverTypes.has(file.type)) {
    ElMessage.error('封面图仅支持 jpg、png 或 webp');
    input.value = '';
    clearCover();
    return;
  }
  if (file.size > maxCoverBytes) {
    ElMessage.error('封面图不能超过 5MB');
    input.value = '';
    clearCover();
    return;
  }

  revokeCoverPreview();
  coverFile.value = file;
  coverPreview.value = URL.createObjectURL(file);
};

const clearCover = () => {
  revokeCoverPreview();
  coverFile.value = null;
  coverPreview.value = '';
  const input = document.getElementById('article-cover-input') as HTMLInputElement | null;
  if (input) {
    input.value = '';
  }
};

const validateForm = () => {
  if (!form.title.trim()) {
    ElMessage.error('请输入文章标题');
    return false;
  }
  if (!form.preview.trim()) {
    ElMessage.error('请输入文章摘要');
    return false;
  }
  if (!form.content.trim()) {
    ElMessage.error('请输入文章正文');
    return false;
  }
  return true;
};

const uploadCover = async () => {
  if (!coverFile.value) {
    return '';
  }

  const data = new FormData();
  data.append('image', coverFile.value);
  const response = await axios.post<UploadArticleCoverResponse>('/uploads/article-cover', data);
  return response.data.cover_image_url;
};

const submitArticle = async () => {
  if (submitting.value || !validateForm()) {
    return;
  }

  submitting.value = true;
  try {
    const coverImageURL = await uploadCover();
    const payload: CreateArticlePayload = {
      title: form.title.trim(),
      preview: form.preview.trim(),
      content: form.content.trim(),
    };
    if (coverImageURL) {
      payload.cover_image_url = coverImageURL;
    }

    const response = await axios.post<Article>('/articles', payload);
    ElMessage.success('文章已发布');
    router.push({ name: 'NewsDetail', params: { id: response.data.ID } });
  } catch (error) {
    console.error('Failed to create article:', error);
    ElMessage.error('文章发布失败，请稍后重试');
  } finally {
    submitting.value = false;
  }
};

const cancelCreate = () => {
  router.push({ name: 'News' });
};

onBeforeUnmount(revokeCoverPreview);
</script>

<style scoped>
.compose-main {
  padding: 36px clamp(18px, 4vw, 48px);
}

.compose-panel {
  max-width: 960px;
  margin: 0 auto;
  padding: 28px;
  border: 1px solid rgba(15, 23, 42, 0.08);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 18px 50px rgba(15, 23, 42, 0.08);
}

.compose-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 24px;
}

.compose-header h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.2;
}

.compose-header p {
  margin: 8px 0 0;
  color: #64748b;
}

.article-form {
  display: grid;
  gap: 8px;
}

.cover-picker {
  display: grid;
  width: min(100%, 420px);
  min-height: 180px;
  place-items: center;
  overflow: hidden;
  border: 1px dashed #94a3b8;
  border-radius: 8px;
  background: #f8fafc;
  cursor: pointer;
}

.cover-input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.cover-empty {
  padding: 0 18px;
  color: #64748b;
  text-align: center;
}

.cover-preview {
  width: 100%;
  height: 240px;
  object-fit: cover;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
}

@media (max-width: 640px) {
  .compose-panel {
    padding: 20px;
  }

  .form-actions {
    justify-content: stretch;
  }

  .form-actions :deep(.el-button) {
    flex: 1;
  }
}
</style>
