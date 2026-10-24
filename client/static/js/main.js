/**
 * Database Management Platform - Client Scripts
 * Handles modal dialogs, flash alerts, and interactive components.
 */

document.addEventListener('DOMContentLoaded', () => {
  // 1. Auto dismiss alerts or manual close
  document.querySelectorAll('.alert-close').forEach(btn => {
    btn.addEventListener('click', (e) => {
      const alert = e.target.closest('.alert');
      if (alert) {
        alert.style.opacity = '0';
        alert.style.transform = 'translateY(-8px)';
        alert.style.transition = 'all 0.2s ease-out';
        setTimeout(() => alert.remove(), 200);
      }
    });
  });

  // 2. Close modal on backdrop click
  document.querySelectorAll('.modal-backdrop').forEach(backdrop => {
    backdrop.addEventListener('click', (e) => {
      if (e.target === backdrop) {
        backdrop.classList.remove('active');
      }
    });
  });

  // 3. Close modal on Escape key
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      document.querySelectorAll('.modal-backdrop.active').forEach(backdrop => {
        backdrop.classList.remove('active');
      });
    }
  });
});

/**
 * Open a modal by ID
 */
function openModal(modalId) {
  const modal = document.getElementById(modalId);
  if (modal) {
    modal.classList.add('active');
    const firstInput = modal.querySelector('input:not([type="hidden"])');
    if (firstInput) {
      setTimeout(() => firstInput.focus(), 50);
    }
  }
}

/**
 * Close a modal by ID
 */
function closeModal(modalId) {
  const modal = document.getElementById(modalId);
  if (modal) {
    modal.classList.remove('active');
  }
}

/**
 * Prepare and open Edit User Modal
 */
function openEditUserModal(id, username, isAdmin, isActive) {
  const modal = document.getElementById('editUserModal');
  if (!modal) return;

  const form = document.getElementById('editUserForm');
  form.action = `/admin/users/${id}/edit`;

  document.getElementById('editUserId').value = id;
  document.getElementById('editUsername').value = username;
  document.getElementById('editIsAdmin').checked = isAdmin === true || isAdmin === 'true';
  document.getElementById('editIsActive').checked = isActive === true || isActive === 'true';

  openModal('editUserModal');
}

/**
 * Prepare and open Reset Password Modal
 */
function openResetPasswordModal(id, username) {
  const modal = document.getElementById('resetPasswordModal');
  if (!modal) return;

  const form = document.getElementById('resetPasswordForm');
  form.action = `/admin/users/${id}/reset-password`;

  document.getElementById('resetPasswordUserId').value = id;
  document.getElementById('resetPasswordUsernameTarget').textContent = username;
  document.getElementById('resetNewPassword').value = '';

  openModal('resetPasswordModal');
}
