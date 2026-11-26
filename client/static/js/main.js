document.addEventListener('DOMContentLoaded', () => {
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

  document.querySelectorAll('.modal-backdrop').forEach(backdrop => {
    backdrop.addEventListener('click', (e) => {
      if (e.target === backdrop) {
        backdrop.classList.remove('active');
      }
    });
  });

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      document.querySelectorAll('.modal-backdrop.active').forEach(backdrop => {
        backdrop.classList.remove('active');
      });
    }
  });
});

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

function closeModal(modalId) {
  const modal = document.getElementById(modalId);
  if (modal) {
    modal.classList.remove('active');
  }
}

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
